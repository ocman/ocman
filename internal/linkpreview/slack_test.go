package linkpreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// slackAPI fakes the Slack Web API per token.
type slackAPI struct {
	mu    sync.Mutex
	calls []string // token method channel
}

func (f *slackAPI) serve(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	q := r.URL.Query()
	f.mu.Lock()
	f.calls = append(f.calls, tok+" "+method+" "+q.Get("channel"))
	f.mu.Unlock()
	reply := func(v map[string]any) {
		if _, ok := v["ok"]; !ok {
			v["ok"] = true
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	host := map[string]string{"tok-alice": "acme", "tok-bob": "other"}[tok]
	if host == "" {
		reply(map[string]any{"ok": false, "error": "token_revoked"})
		return
	}
	switch method {
	case "auth.test":
		reply(map[string]any{"url": "https://" + host + ".slack.com/", "team": host, "team_id": "T-" + host, "user": tok})
	case "conversations.info":
		switch q.Get("channel") {
		case "C1":
			reply(map[string]any{"channel": map[string]any{"name": "general"}})
		case "C2":
			reply(map[string]any{"channel": map[string]any{"name": "secret", "is_private": true}})
		default:
			reply(map[string]any{"ok": false, "error": "missing_scope"})
		}
	case "conversations.history":
		reply(map[string]any{"messages": []any{map[string]any{
			"ts": q.Get("latest"), "user": "U1", "reply_count": 2, "text": "hi <@U2> see <https://x.test|docs> &amp; more",
		}}})
	case "conversations.replies":
		reply(map[string]any{"messages": []any{
			map[string]any{"ts": q.Get("ts"), "user": "U1", "text": "parent"},
			map[string]any{"ts": q.Get("latest"), "username": "deploybot", "text": "reply text"},
		}})
	case "users.info":
		reply(map[string]any{"user": map[string]any{"name": "ann", "profile": map[string]any{"display_name": "Ann"}}})
	default:
		reply(map[string]any{"ok": false, "error": "unknown_method"})
	}
}

func newSlackHarness(t *testing.T) (*Service, *slackAPI, *fakeTokens, Slack, *httptest.Server) {
	api := &slackAPI{}
	srv := httptest.NewTLSServer(http.HandlerFunc(api.serve))
	t.Cleanup(srv.Close)
	tokens := &fakeTokens{grants: map[string]string{"alice/T-acme": "tok-alice", "bob/T-other": "tok-bob", "eve/T-acme": "tok-gone"}}
	res := Slack{APIBase: srv.URL + "/api"}
	return New(tokens, srv.Client(), res), api, tokens, res, srv
}

func slackResolve(svc *Service, viewer, text string) []Preview {
	refs := svc.Discover(text, nil)
	return svc.Resolve(context.Background(), viewer, "owner", refs)
}

func TestSlackParseURL(t *testing.T) {
	text := "https://acme.slack.com/archives/C1/p1700000000000100 " +
		"https://acme.slack.com/archives/C1/p1700000000000200?thread_ts=1700000000.000100&cid=C1 " +
		"https://acme.slack.com/archives/C1/p1700000000000100?thread_ts=1700000000.000100 " + // parent: dup
		"http://acme.slack.com/archives/C1/p1700000000000300 https://evil.com/archives/C1/p1700000000000100 " +
		"https://acme.slack.com.evil.com/archives/C1/p1700000000000100 https://acme.slack.com/archives/c1/p1 " +
		"https://acme.slack.com/archives/C1/p1700000000000400/../x"
	var got []string
	for _, r := range Discover(text, []Resolver{Slack{}}, nil) {
		got = append(got, r.Kind+" "+r.ID+" "+r.URL)
	}
	want := []string{
		"message C1/p1700000000000100 https://acme.slack.com/archives/C1/p1700000000000100",
		"reply C1/p1700000000000200 https://acme.slack.com/archives/C1/p1700000000000200?cid=C1&thread_ts=1700000000.000100",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("refs:\n%s", strings.Join(got, "\n"))
	}
}

func TestSlackPublicMessageAndThreadReply(t *testing.T) {
	svc, _, _, _, _ := newSlackHarness(t)
	ps := slackResolve(svc, "alice", "https://acme.slack.com/archives/C1/p1700000000000100 "+
		"https://acme.slack.com/archives/C1/p1700000000000200?thread_ts=1700000000.000100&cid=C1")
	if len(ps) != 2 {
		t.Fatalf("previews = %+v", ps)
	}
	msg, reply := ps[0], ps[1]
	if msg.State != StateOK || msg.Title != "hi @U2 see docs & more" || msg.Icon != "bi-slack" ||
		strings.Join(msg.Meta, "|") != "#general|Ann|2 replies" || msg.UpdatedAt.Unix() != 1700000000 {
		t.Fatalf("message = %+v", msg)
	}
	if reply.State != StateOK || reply.Title != "reply text" || reply.Status != "Thread reply" ||
		strings.Join(reply.Meta, "|") != "#general|deploybot" || !strings.Contains(reply.URL, "thread_ts=") {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestSlackPrivateAndDMDenied(t *testing.T) {
	svc, api, _, _, _ := newSlackHarness(t)
	ps := slackResolve(svc, "alice", "https://acme.slack.com/archives/C2/p1700000000000100 "+
		"https://acme.slack.com/archives/D1/p1700000000000100 https://acme.slack.com/archives/G1/p1700000000000100")
	for _, p := range ps {
		if p.State != StateDenied || p.Title != "" || p.Meta != nil {
			t.Fatalf("private preview leaked: %+v", p)
		}
	}
	for _, c := range api.calls {
		if strings.Contains(c, "history") || strings.Contains(c, "replies") {
			t.Fatalf("private messages were read: %s", c)
		}
	}
}

func TestSlackViewerIsolationAndRevocation(t *testing.T) {
	svc, api, tokens, _, _ := newSlackHarness(t)
	link := "https://acme.slack.com/archives/C1/p1700000000000100"
	if p := slackResolve(svc, "alice", link)[0]; p.State != StateOK {
		t.Fatalf("alice = %+v", p)
	}
	// No grant: nothing cached for alice is served.
	if p := slackResolve(svc, "mallory", link)[0]; p.State != StateConnect || p.Title != "" {
		t.Fatalf("mallory = %+v", p)
	}
	// Bob's grant is for another workspace: never used to read acme.
	if p := slackResolve(svc, "bob", link)[0]; p.State != StateNotFound || p.Title != "" {
		t.Fatalf("bob = %+v", p)
	}
	for _, c := range api.calls {
		if strings.HasPrefix(c, "tok-bob") && !strings.Contains(c, "auth.test") {
			t.Fatalf("bob's token read content: %s", c)
		}
	}
	// A revoked token forgets the grant and asks to reconnect.
	if p := slackResolve(svc, "eve", link)[0]; p.State != StateConnect {
		t.Fatalf("eve = %+v", p)
	}
	if strings.Join(tokens.revoked, ",") != "eve/T-acme" {
		t.Fatalf("revoked = %v", tokens.revoked)
	}
}

func TestSlackOAuthUserTokenOnly(t *testing.T) {
	_, _, _, res, srv := newSlackHarness(t)
	p := SlackOAuth("cid", "secret", res.APIBase)
	if p.AuthParams["user_scope"] != "channels:history,channels:read,users:read" || len(p.Scopes) != 0 {
		t.Fatalf("scopes = %v %v", p.AuthParams, p.Scopes)
	}
	grants, err := p.Identify(context.Background(), srv.Client(), previewauth.Token{AccessToken: "tok-alice"})
	if err != nil || len(grants) != 1 || grants[0].WorkspaceID != "T-acme" || grants[0].AccountName != "tok-alice" {
		t.Fatalf("grants = %+v, %v", grants, err)
	}
	if _, err := p.Identify(context.Background(), srv.Client(), previewauth.Token{AccessToken: "tok-gone"}); err == nil {
		t.Fatal("revoked token identified")
	}

	user := map[string]any{"access_token": "xoxp-1", "refresh_token": "r", "expires_in": 60.0}
	for name, tc := range map[string]struct {
		raw  map[string]any
		want string
		err  error
	}{
		"authed user":   {map[string]any{"ok": true, "access_token": "xoxb-bot", "authed_user": user}, "xoxp-1", nil},
		"refresh":       {map[string]any{"ok": true, "access_token": "xoxe.xoxp-2", "token_type": "user"}, "xoxe.xoxp-2", nil},
		"bot only":      {map[string]any{"ok": true, "access_token": "xoxb-bot", "token_type": "bot"}, "", previewauth.ErrExchange},
		"revoked":       {map[string]any{"ok": false, "error": "invalid_refresh_token"}, "", previewauth.ErrRevoked},
		"access denied": {map[string]any{"ok": false, "error": "access_denied"}, "", previewauth.ErrExchange},
	} {
		fields, err := slackToken(tc.raw)
		if !errors.Is(err, tc.err) || (err == nil && fields["access_token"] != tc.want) {
			t.Fatalf("%s: %v %v", name, fields, err)
		}
	}
}
