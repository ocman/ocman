package webhook

import (
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestMatchesHeaderAndJSONPointerPredicates(t *testing.T) {
	e := relay.InboxEnvelope{Body: []byte(`{"event":{"kind":"push"},"items":["a","b"]}`), Request: relay.InboxRequest{Header: http.Header{"X-Event": {"PUSH"}}}}
	trueValue := true
	sub := state.WebhookSubscription{HeaderPredicatesJSON: `{"x-event":{"oneOf":["push","merge"]}}`, JSONPredicatesJSON: `{"/event/kind":{"equals":"push"},"/items/1":{"exists":true}}`}
	if ok, err := matches(e, sub); err != nil || !ok {
		t.Fatalf("matches = %v, %v", ok, err)
	}
	sub.JSONPredicatesJSON = `{"/event~1kind":{"exists":true}}`
	if ok, err := matches(e, sub); err != nil || ok {
		t.Fatalf("escaped pointer matches = %v, %v", ok, err)
	}
	if !predicateMatch([]string{"yes"}, true, predicate{Exists: &trueValue}, true) {
		t.Fatal("exists predicate did not match")
	}
}

func TestMatchesInvalidJSONIsIgnored(t *testing.T) {
	e := relay.InboxEnvelope{Body: []byte("not json")}
	if ok, err := matches(e, state.WebhookSubscription{JSONPredicatesJSON: `{"/x":{"exists":true}}`}); err != nil || ok {
		t.Fatalf("invalid JSON = %v, %v", ok, err)
	}
}
