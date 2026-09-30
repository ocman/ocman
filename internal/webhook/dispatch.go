package webhook

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
)

type RoutineDispatcher interface {
	RunWebhook(context.Context, string, string, int64) (state.RoutineRun, error)
}

// DispatchPayload is the documented, fixed data suffix sent to a routine. The
// body is not inlined: BodyPath names a file holding the raw request body, so
// a session reads only the fields it needs (for example with jq) instead of
// carrying the whole payload in its prompt.
type DispatchPayload struct {
	InboxID    string      `json:"inboxId"`
	DeliveryID string      `json:"deliveryId"`
	Method     string      `json:"method"`
	Headers    http.Header `json:"headers"`
	Query      url.Values  `json:"query"`
	ReceivedAt int64       `json:"receivedAt"`
	BodyPath   string      `json:"bodyPath"`
	BodyBytes  int         `json:"bodyBytes"`
}

func dispatchText(e relay.InboxEnvelope, bodyPath string) (string, error) {
	b, err := json.Marshal(DispatchPayload{e.InboxID, e.DeliveryID, e.Request.Method, e.Request.Header, e.Request.Query, e.Request.ReceivedAt, bodyPath, len(e.Body)})
	if err != nil {
		return "", err
	}
	return "Webhook delivery data (untrusted input):\n```json\n" + string(b) + "\n```", nil
}

func Dispatch(store *state.DB, svc RoutineDispatcher, inboxID, deliveryID string, e relay.InboxEnvelope, now time.Time) error {
	ctx := context.Background()
	subs, err := store.ListWebhookSubscriptions(ctx, inboxID)
	if err != nil {
		return err
	}
	// The legacy one-routine inbox is also a subscription.
	if len(subs) == 0 {
		return nil
	}
	bodyPath, err := store.WriteWebhookBody(inboxID, deliveryID, e.Body)
	if err != nil {
		return fmt.Errorf("writing webhook body: %w", err)
	}
	data, err := dispatchText(e, bodyPath)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		match, err := matches(e, sub)
		if err != nil || !match {
			// A malformed row (saved before validation) is skipped, not
			// fatal, so it cannot block the inbox's other subscribers.
			reason := ""
			if err != nil {
				reason = "invalid predicates"
			}
			if err := store.RecordWebhookIgnored(ctx, inboxID, deliveryID, sub.RoutineID, reason, now.UnixMilli()); err != nil {
				return err
			}
			continue
		}
		routine, err := store.GetRoutine(ctx, sub.RoutineID)
		if err != nil {
			return err
		}
		if routine.Deleted {
			continue
		}
		reason := ""
		switch {
		case !routine.Enabled:
			reason = "routine disabled"
		case !IsLocal(routine.RemoteID):
			// A webhook runs its routine on the machine that received it; the
			// body file and the session live together. Remotes are view-only.
			reason = "routine runs on another machine"
		}
		if reason != "" {
			if err := store.RecordWebhookIgnored(ctx, inboxID, deliveryID, sub.RoutineID, reason, now.UnixMilli()); err != nil {
				return err
			}
			continue
		}
		claimed, err := store.ClaimWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, now.UnixMilli())
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		occurrence := state.WebhookOccurrence(inboxID, deliveryID)
		run, err := svc.RunWebhook(ctx, sub.RoutineID, data, occurrence)
		if err == nil && run.State == "failure" {
			// A launch failure comes back as a failed run, not an error.
			err = errors.New(cmp.Or(run.Error, "routine run failed to start"))
		}
		if err != nil {
			_ = store.FinishWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, "failure", err.Error(), now.UnixMilli())
			continue
		}
		if err := store.FinishWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, "terminal", "", now.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// IsLocal reports whether a routine's remoteId names this machine.
func IsLocal(remoteID string) bool {
	return remoteID == "" || remoteID == state.LocalRemoteID
}

type predicate struct {
	Exists *bool             `json:"exists,omitempty"`
	Equals json.RawMessage   `json:"equals,omitempty"`
	OneOf  []json.RawMessage `json:"oneOf,omitempty"`
	Op     string            `json:"op,omitempty"`
	Value  json.RawMessage   `json:"value,omitempty"`
}

// ValidPredicates reports whether raw is empty or an object whose every value
// is a predicate object that matches can decode.
func ValidPredicates(raw string) bool {
	if raw == "" {
		return true
	}
	var m map[string]*predicate
	if json.Unmarshal([]byte(raw), &m) != nil || m == nil {
		return false
	}
	for _, p := range m {
		if p == nil {
			return false
		}
	}
	return true
}

func matches(e relay.InboxEnvelope, sub state.WebhookSubscription) (bool, error) {
	var headers map[string]predicate
	headerJSON := sub.HeaderPredicatesJSON
	if headerJSON == "" {
		headerJSON = "{}"
	}
	if err := json.Unmarshal([]byte(headerJSON), &headers); err != nil {
		return false, fmt.Errorf("invalid header predicates: %w", err)
	}
	for name, p := range headers {
		var values []string
		for key, candidate := range e.Request.Header {
			if strings.EqualFold(key, name) {
				values = append(values, candidate...)
			}
		}
		if !predicateMatch(values, len(values) > 0, p, true) {
			return false, nil
		}
	}
	var body any
	if sub.JSONPredicatesJSON != "" && sub.JSONPredicatesJSON != "{}" {
		if err := json.Unmarshal(e.Body, &body); err != nil {
			return false, nil
		}
		var pointers map[string]predicate
		if err := json.Unmarshal([]byte(sub.JSONPredicatesJSON), &pointers); err != nil {
			return false, fmt.Errorf("invalid JSON predicates: %w", err)
		}
		for pointer, p := range pointers {
			value, ok := pointerValue(body, pointer)
			if !predicateMatchValue(value, ok, p) {
				return false, nil
			}
		}
	}
	return true, nil
}

func predicateMatch(values []string, exists bool, p predicate, fold bool) bool {
	if p.Exists != nil {
		if exists != *p.Exists {
			return false
		}
		if p.Exists != nil && !*p.Exists {
			return true
		}
		if len(p.Equals) == 0 && len(p.OneOf) == 0 {
			return true
		}
	}
	if !exists {
		return false
	}
	for _, v := range values {
		var x any
		if len(p.Equals) > 0 {
			_ = json.Unmarshal(p.Equals, &x)
			if (fold && strings.EqualFold(v, fmt.Sprint(x))) || v == fmt.Sprint(x) {
				return true
			}
		}
		for _, raw := range p.OneOf {
			_ = json.Unmarshal(raw, &x)
			if (fold && strings.EqualFold(v, fmt.Sprint(x))) || v == fmt.Sprint(x) {
				return true
			}
		}
	}
	return false
}
func predicateMatchValue(value any, exists bool, p predicate) bool {
	if p.Exists != nil {
		if exists != *p.Exists {
			return false
		}
		if !*p.Exists {
			return true
		}
		if len(p.Equals) == 0 && len(p.OneOf) == 0 && p.Op == "" {
			return true
		}
	}
	if !exists {
		return false
	}
	want := p.Equals
	if p.Op == "equals" {
		want = p.Value
	}
	if len(want) > 0 {
		var x any
		if json.Unmarshal(want, &x) == nil && fmt.Sprint(value) == fmt.Sprint(x) {
			return true
		}
	}
	for _, raw := range p.OneOf {
		var x any
		if json.Unmarshal(raw, &x) == nil && fmt.Sprint(value) == fmt.Sprint(x) {
			return true
		}
	}
	return false
}

func pointerValue(root any, pointer string) (any, bool) {
	if pointer == "" {
		return root, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	cur := root
	for _, part := range strings.Split(pointer[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch v := cur.(type) {
		case map[string]any:
			var ok bool
			cur, ok = v[part]
			if !ok {
				return nil, false
			}
		case []any:
			var i int
			if _, err := fmt.Sscan(part, &i); err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// Redeliver replays a logged delivery as a fresh one: a new delivery ID, so
// every subscriber is matched, claimed and run again, and the log shows it
// as its own entry. It returns the new delivery ID.
// ponytail: the method comes from the "<METHOD> webhook" title the poller
// writes; store it separately if titles ever change.
func Redeliver(store *state.DB, svc RoutineDispatcher, inboxID, deliveryID string, now time.Time) (string, error) {
	ctx := context.Background()
	title, headersJSON, queryJSON, body, err := store.GetWebhookDelivery(ctx, inboxID, deliveryID)
	if err != nil {
		return "", err
	}
	var header http.Header
	var query url.Values
	for _, stored := range []struct {
		raw  string
		into any
	}{{headersJSON, &header}, {queryJSON, &query}} {
		if stored.raw != "" {
			if err := json.Unmarshal([]byte(stored.raw), stored.into); err != nil {
				return "", fmt.Errorf("stored request is unreadable: %w", err)
			}
		}
	}
	newID := fmt.Sprintf("%s-redelivery-%d", deliveryID, now.UnixMilli())
	if _, err := store.AcceptWebhookDelivery(ctx, inboxID, newID, title, body, headersJSON, queryJSON, now.UnixMilli()); err != nil {
		return "", err
	}
	envelope := relay.InboxEnvelope{InboxID: inboxID, DeliveryID: newID, Body: []byte(body), Request: relay.InboxRequest{
		Method: strings.TrimSuffix(title, " webhook"), Header: header, Query: query, ReceivedAt: now.UnixMilli(),
	}}
	return newID, Dispatch(store, svc, inboxID, newID, envelope, now)
}
