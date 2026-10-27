package webhook

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
)

type RoutineDispatcher interface {
	RunWebhook(context.Context, string, string, string, int64) (state.RoutineRun, error)
}

// DispatchPayload is the documented, fixed data suffix sent to a routine.
// Headers and body are not inlined: HeadersPath and BodyPath name files a
// session queries for just the fields it needs (for example with jq) instead
// of carrying the whole request in its prompt.
type DispatchPayload struct {
	InboxID     string     `json:"inboxId"`
	DeliveryID  string     `json:"deliveryId"`
	Method      string     `json:"method"`
	Query       url.Values `json:"query"`
	ReceivedAt  int64      `json:"receivedAt"`
	HeadersPath string     `json:"headersPath"`
	BodyPath    string     `json:"bodyPath"`
	BodyBytes   int        `json:"bodyBytes"`
}

func dispatchText(e relay.InboxEnvelope, headersPath, bodyPath string) (string, error) {
	b, err := json.Marshal(DispatchPayload{e.InboxID, e.DeliveryID, e.Request.Method, e.Request.Query, e.Request.ReceivedAt, headersPath, bodyPath, len(e.Body)})
	if err != nil {
		return "", err
	}
	return "Webhook delivery data (untrusted input):\n```json\n" + string(b) + "\n```", nil
}

// Dispatch runs the delivery's matching subscriptions under ctx. A
// cancelled ctx stops before the next claim; a claimed dispatch still
// records its outcome, so it is never left claimed without a result.
func Dispatch(ctx context.Context, store *state.DB, svc RoutineDispatcher, inboxID, deliveryID string, e relay.InboxEnvelope, now time.Time) error {
	subs, err := store.ListWebhookSubscriptions(ctx, inboxID)
	if err != nil {
		return err
	}
	// The legacy one-routine inbox is also a subscription.
	if len(subs) == 0 {
		return nil
	}
	headersPath, bodyPath, err := writeDeliveryFiles(store, inboxID, deliveryID, e)
	if err != nil {
		return err
	}
	data, err := dispatchText(e, headersPath, bodyPath)
	if err != nil {
		return err
	}
	label := deliveryLabel(e.Body)
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
		if err := ctx.Err(); err != nil {
			return err
		}
		claimed, err := store.ClaimWebhookDispatch(ctx, inboxID, deliveryID, sub.RoutineID, now.UnixMilli())
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		occurrence := state.WebhookOccurrence(inboxID, deliveryID)
		run, err := svc.RunWebhook(ctx, sub.RoutineID, data, label, occurrence)
		receipt := context.WithoutCancel(ctx)
		if err == nil && run.State == "failure" {
			// A launch failure comes back as a failed run, not an error.
			err = errors.New(cmp.Or(run.Error, "routine run failed to start"))
		}
		if err != nil {
			_ = store.FinishWebhookDispatch(receipt, inboxID, deliveryID, sub.RoutineID, "failure", err.Error(), now.UnixMilli())
			continue
		}
		if err := store.FinishWebhookDispatch(receipt, inboxID, deliveryID, sub.RoutineID, "terminal", "", now.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// writeDeliveryFiles stores the headers (a JSON object of canonical header name
// to values, e.g. {"X-Forgejo-Event":["pull_request"]}) and the raw body.
func writeDeliveryFiles(store *state.DB, inboxID, deliveryID string, e relay.InboxEnvelope) (string, string, error) {
	headers := http.Header{}
	for name, values := range e.Request.Header {
		key := http.CanonicalHeaderKey(name)
		headers[key] = append(headers[key], values...)
	}
	raw, err := json.Marshal(headers)
	if err != nil {
		return "", "", err
	}
	headersPath, err := store.WriteWebhookFile(inboxID, deliveryID, state.WebhookHeadersFile, raw)
	if err != nil {
		return "", "", fmt.Errorf("writing webhook headers: %w", err)
	}
	bodyPath, err := store.WriteWebhookFile(inboxID, deliveryID, state.WebhookBodyFile, e.Body)
	if err != nil {
		return "", "", fmt.Errorf("writing webhook body: %w", err)
	}
	return headersPath, bodyPath, nil
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
		p.Op = "" // header predicates have no op form
		if !predicateMatch(p, len(values) > 0, func(want any) bool {
			return slices.ContainsFunc(values, func(v string) bool { return headerEquals(v, want, true) })
		}) {
			return false, nil
		}
	}
	if sub.JSONPredicatesJSON != "" && sub.JSONPredicatesJSON != "{}" {
		body, err := decodeJSON(e.Body)
		if err != nil {
			return false, nil
		}
		var pointers map[string]predicate
		if err := json.Unmarshal([]byte(sub.JSONPredicatesJSON), &pointers); err != nil {
			return false, fmt.Errorf("invalid JSON predicates: %w", err)
		}
		for pointer, p := range pointers {
			value, ok := pointerValue(body, pointer)
			if !predicateMatch(p, ok, func(want any) bool { return jsonEqual(value, want) }) {
				return false, nil
			}
		}
	}
	return true, nil
}

// predicateMatch applies p's exists, equals and oneOf rules to a value that
// exists or not; equal compares it with one decoded expected value. The op
// form ("equals" with value) applies to JSON body predicates only.
func predicateMatch(p predicate, exists bool, equal func(want any) bool) bool {
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
	wants := p.OneOf
	if len(want) > 0 {
		wants = append([]json.RawMessage{want}, wants...)
	}
	for _, raw := range wants {
		if x, err := decodeJSON(raw); err == nil && equal(x) {
			return true
		}
	}
	return false
}

// headerEquals compares a header's text with an expected predicate value. A
// header is text: only a scalar expected value can equal it. Strings and
// booleans compare as text; a number matches a header that is exactly a JSON
// number with the same decimal value, the rule a string body value follows.
func headerEquals(v string, want any, fold bool) bool {
	var text string
	switch w := want.(type) {
	case string:
		text = w
	case bool:
		text = strconv.FormatBool(w)
	case json.Number:
		return scalarEqualsText(w, v)
	default:
		return false
	}
	return v == text || (fold && strings.EqualFold(v, text))
}

// decodeJSON decodes one JSON value, keeping numbers as their exact text
// (json.Number) so no comparison is made through a rounded float64.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after JSON value")
	}
	return v, nil
}

// jsonEqual compares a body value with an expected predicate value, both
// decoded by decodeJSON. Equal types compare by value (objects and arrays
// deeply, numbers by exact decimal value). The routine form stores every
// typed value as a string, so a string also equals a scalar whose JSON text
// it is: "5" or "5.0" equals 5, "true" equals true and "null" equals null.
// Composite values never equal a string.
func jsonEqual(got, want any) bool {
	if s, ok := want.(string); ok {
		if g, ok := got.(string); ok {
			return g == s
		}
		return scalarEqualsText(got, s)
	}
	if s, ok := got.(string); ok {
		return scalarEqualsText(want, s)
	}
	return sameJSON(got, want)
}

// sameJSON is strictly typed structural equality, with numbers compared by
// exact decimal value so 5 and 5.0 are equal but 2^53 and 2^53+1 are not.
func sameJSON(got, want any) bool {
	switch w := want.(type) {
	case json.Number:
		g, ok := got.(json.Number)
		return ok && scalarEqualsText(w, g.String())
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !sameJSON(g[i], w[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok || !sameJSON(gv, wv) {
				return false
			}
		}
		return true
	default:
		// A string, boolean or null: comparable, and unequal to any value of
		// another dynamic type.
		return got == want
	}
}

func scalarEqualsText(v any, s string) bool {
	switch v := v.(type) {
	case nil:
		return s == "null"
	case bool:
		return s == strconv.FormatBool(v)
	case json.Number:
		want, ok := canonicalNumber(s)
		if !ok {
			return false
		}
		got, ok := canonicalNumber(v.String())
		return ok && got == want
	default:
		return false
	}
}

// canonicalNumber reduces s, only when it is exactly a JSON number (without
// the signs, prefixes, separators or spaces strconv would otherwise accept),
// to its sign, significant digits and exponent. Two spellings of one decimal
// value ("5", "5.0", "50e-1") share a key and distinct values never do. The
// value itself is never materialised, so a huge exponent costs nothing.
func canonicalNumber(s string) (string, bool) {
	if s == "" || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) || strings.TrimSpace(s) != s || !json.Valid([]byte(s)) {
		return "", false
	}
	sign := ""
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		sign, s = "-", rest
	}
	mantissa, expText, _ := strings.Cut(strings.ToLower(s), "e")
	whole, frac, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		return "0", true // -0 equals 0
	}
	exp := new(big.Int)
	if expText != "" {
		exp.SetString(expText, 10) // json.Valid checked its digits
	}
	significant := strings.TrimRight(digits, "0")
	exp.Add(exp, big.NewInt(int64(len(digits)-len(significant)-len(frac))))
	return sign + significant + "e" + exp.String(), true
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
		part, ok := unescapePointerToken(part)
		if !ok {
			return nil, false
		}
		switch v := cur.(type) {
		case map[string]any:
			cur, ok = v[part]
			if !ok {
				return nil, false
			}
		case []any:
			i, ok := pointerIndex(part)
			if !ok || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// unescapePointerToken decodes one RFC 6901 reference token: "~1" is "/" and
// "~0" is "~", decoded in that order so "~01" stays "~1". Any other "~" is an
// invalid escape and the pointer matches nothing.
func unescapePointerToken(token string) (string, bool) {
	if !strings.Contains(token, "~") {
		return token, true
	}
	var b strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			b.WriteByte(token[i])
			continue
		}
		if i+1 >= len(token) {
			return "", false
		}
		switch token[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", false
		}
		i++
	}
	return b.String(), true
}

// pointerIndex parses an RFC 6901 array index: "0" or a decimal without a
// leading zero, sign, spaces or separators. "-" (past the end) never exists.
func pointerIndex(token string) (int, bool) {
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, false
	}
	n := 0
	for _, c := range token {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<31 {
			return 0, false // beyond any decodable array
		}
	}
	return n, true
}

// Redeliver replays a logged delivery as a fresh one: a new delivery ID, so
// every subscriber is matched, claimed and run again, and the log shows it
// as its own entry. It returns the new delivery ID.
// ponytail: the method comes from the "<METHOD> webhook" title the poller
// writes; store it separately if titles ever change.
func Redeliver(ctx context.Context, store *state.DB, svc RoutineDispatcher, inboxID, deliveryID string, now time.Time) (string, error) {
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
	return newID, Dispatch(ctx, store, svc, inboxID, newID, envelope, now)
}

// deliveryLabel names the delivery's item for the run title, e.g. "#12 opened"
// for a forge pull request or issue event. The body is untrusted: only a
// positive integer number and a short lowercase action are used.
func deliveryLabel(body []byte) string {
	var item struct {
		Number json.RawMessage `json:"number"`
		Action string          `json:"action"`
	}
	if json.Unmarshal(body, &item) != nil {
		return ""
	}
	number, err := strconv.ParseUint(string(item.Number), 10, 32)
	if err != nil || number == 0 {
		return ""
	}
	label := "#" + strconv.FormatUint(number, 10)
	if len(item.Action) <= 32 && item.Action != "" && strings.Trim(item.Action, "abcdefghijklmnopqrstuvwxyz_") == "" {
		label += " " + item.Action
	}
	return label
}
