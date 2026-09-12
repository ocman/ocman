package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
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

// DispatchPayload is the documented, fixed data suffix sent to a routine.
type DispatchPayload struct {
	InboxID    string      `json:"inboxId"`
	DeliveryID string      `json:"deliveryId"`
	Method     string      `json:"method"`
	Headers    http.Header `json:"headers"`
	Query      url.Values  `json:"query"`
	ReceivedAt int64       `json:"receivedAt"`
	BodyBase64 string      `json:"bodyBase64"`
}

func dispatchText(e relay.InboxEnvelope) (string, error) {
	b, err := json.Marshal(DispatchPayload{e.InboxID, e.DeliveryID, e.Request.Method, e.Request.Header, e.Request.Query, e.Request.ReceivedAt, base64.StdEncoding.EncodeToString(e.Body)})
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
	data, err := dispatchText(e)
	if err != nil {
		return err
	}
	matched := false
	for _, sub := range subs {
		match, err := matches(e, sub)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		routine, err := store.GetRoutine(ctx, sub.RoutineID)
		if err != nil {
			return err
		}
		if routine.Deleted || !routine.Enabled {
			continue
		}
		matched = true
		claimed, err := store.ClaimWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, now.UnixMilli())
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(inboxID + ":" + deliveryID))
		occurrence := int64(h.Sum64() & 0x7fffffffffffffff)
		if occurrence == 0 {
			occurrence = 1
		}
		_, err = svc.RunWebhook(ctx, sub.RoutineID, data, occurrence)
		if err != nil {
			_ = store.FinishWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, "failure", err.Error(), now.UnixMilli())
			continue
		}
		if err := store.FinishWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, "terminal", "", now.UnixMilli()); err != nil {
			return err
		}
	}
	if !matched {
		return store.RecordWebhookIgnored(ctx, inboxID, deliveryID, now.UnixMilli())
	}
	return nil
}

type predicate struct {
	Exists *bool             `json:"exists,omitempty"`
	Equals json.RawMessage   `json:"equals,omitempty"`
	OneOf  []json.RawMessage `json:"oneOf,omitempty"`
	Op     string            `json:"op,omitempty"`
	Value  json.RawMessage   `json:"value,omitempty"`
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
