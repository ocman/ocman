package webhook

import (
	"encoding/json"
	"math/big"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
)

// rfc6901 is a reference RFC 6901 evaluator over decoded JSON: "~1" is "/",
// "~0" is "~", any other "~" is invalid, and an array index is "0" or a
// decimal without leading zeros.
func rfc6901(root any, pointer string) (any, bool) {
	if pointer == "" {
		return root, true
	}
	if pointer[0] != '/' {
		return nil, false
	}
	cur := root
	for _, token := range strings.Split(pointer[1:], "/") {
		for i := 0; i < len(token); i++ {
			if token[i] == '~' && (i+1 == len(token) || (token[i+1] != '0' && token[i+1] != '1')) {
				return nil, false
			}
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[token]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			if token == "" || (len(token) > 1 && token[0] == '0') || strings.Trim(token, "0123456789") != "" {
				return nil, false
			}
			i, err := strconv.Atoi(token)
			if err != nil || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// FuzzPointerValue checks webhook JSON predicates resolve pointers strictly
// per RFC 6901: no lenient array indexes ("01", "+1", "1abc") and no
// invalid escapes.
func FuzzPointerValue(f *testing.F) {
	f.Add(`{"a/b":{"m~n":[1,2,3]}}`, "/a~1b/m~0n/2")
	f.Add(`{"items":[10,20]}`, "/items/01")
	f.Add(`{"items":[10,20]}`, "/items/0x1")
	f.Add(`{"items":[10,20]}`, "/items/+1")
	f.Add(`{"items":[10,20]}`, "/items/-")
	f.Add(`{"~2":1,"":{"":2}}`, "/~2")
	f.Add(`{"":{"":2}}`, "//")
	f.Add(`{"action":"opened"}`, "action")
	f.Fuzz(func(t *testing.T, doc, pointer string) {
		var root any
		if json.Unmarshal([]byte(doc), &root) != nil {
			return
		}
		got, gotOK := pointerValue(root, pointer)
		want, wantOK := rfc6901(root, pointer)
		if gotOK != wantOK || (gotOK && !reflect.DeepEqual(got, want)) {
			t.Fatalf("pointer %q on %s: got (%v, %v), want (%v, %v)", pointer, doc, got, gotOK, want, wantOK)
		}
	})
}

// FuzzEqualsPredicate checks an equals predicate against one body value
// and through a whole subscription, against a reference that compares
// numbers as exact rationals. Equal JSON values always match and unequal
// ones never do, except the documented string form of a scalar (the
// routine form stores typed values as strings: "5" equals 5, "true" equals
// true, "null" equals null); composites never equal a string. Any predicate
// set ValidPredicates accepts must evaluate without error.
func FuzzEqualsPredicate(f *testing.F) {
	f.Add(`"1"`, `1`)
	f.Add(`"true"`, `true`)
	f.Add(`["a b"]`, `"[a b]"`)
	f.Add(`"1e2"`, `100`)
	f.Add(`" 5"`, `5`)
	f.Add(`{"a":[1]}`, `{"a":[1.0]}`)
	f.Add(`null`, `"null"`)
	f.Add(`"9007199254740993"`, `9007199254740992`)
	f.Add(`0.1`, `0.1000000000000000000001`)
	f.Fuzz(func(t *testing.T, want, have string) {
		if hugeExponent(want) || hugeExponent(have) {
			return // the big.Rat reference would materialise the value
		}
		w, errW := decodeJSON([]byte(want))
		h, errH := decodeJSON([]byte(have))
		if errW != nil || errH != nil {
			return
		}
		matched := predicateMatchValue(h, true, predicate{Equals: json.RawMessage(want)})
		if equal := refEqual(w, h); equal && !matched {
			t.Fatalf("equals %s did not match the equal value %s", want, have)
		} else if matched && !equal && !scalarTextOf(w, h) && !scalarTextOf(h, w) {
			t.Fatalf("equals %s matched the unequal value %s", want, have)
		}

		predicates, err := json.Marshal(map[string]map[string]json.RawMessage{"/v": {"equals": json.RawMessage(want)}})
		if err != nil || !ValidPredicates(string(predicates)) {
			t.Fatalf("predicates %s rejected: %v", predicates, err)
		}
		e := relay.InboxEnvelope{
			Body:    []byte(`{"v":` + have + `}`),
			Request: relay.InboxRequest{Header: http.Header{"X-V": {have}}},
		}
		sub := state.WebhookSubscription{
			HeaderPredicatesJSON: strings.Replace(string(predicates), `"/v"`, `"x-v"`, 1),
			JSONPredicatesJSON:   string(predicates),
		}
		if _, err := matches(e, sub); err != nil {
			t.Fatalf("valid predicates %s failed to evaluate: %v", predicates, err)
		}
		if jsonOnly, err := matches(e, state.WebhookSubscription{JSONPredicatesJSON: sub.JSONPredicatesJSON}); err != nil || jsonOnly != matched {
			t.Fatalf("subscription match %v (%v), value match %v", jsonOnly, err, matched)
		}
	})
}

// hugeExponent reports whether s contains an exponent too long for the
// big.Rat reference to expand cheaply.
func hugeExponent(s string) bool {
	return regexp.MustCompile(`[eE][+-]?[0-9]{4,}`).MatchString(s)
}

// refEqual is strictly typed JSON equality over decodeJSON values, with
// numbers compared as exact rationals.
func refEqual(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		b, ok := b.(json.Number)
		return ok && refNumber(a.String()).Cmp(refNumber(b.String())) == 0
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !refEqual(a[i], b[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for k, av := range a {
			if bv, ok := b[k]; !ok || !refEqual(av, bv) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func refNumber(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("decodeJSON produced an invalid number: " + s)
	}
	return r
}

// scalarTextOf reports whether s is a string that is exactly the JSON text
// of a value equal to the scalar v (a number, boolean or null).
func scalarTextOf(s, v any) bool {
	text, ok := s.(string)
	if !ok || strings.TrimSpace(text) != text {
		return false
	}
	switch v.(type) {
	case nil, bool, json.Number:
	default:
		return false
	}
	parsed, err := decodeJSON([]byte(text))
	return err == nil && refEqual(parsed, v)
}
