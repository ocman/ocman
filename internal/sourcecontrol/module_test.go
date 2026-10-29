package sourcecontrol

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type fakeClient struct {
	forge.Forge
	token string
	err   error
}

func (c fakeClient) CurrentUser(context.Context) (forge.CurrentUser, error) {
	if c.token == "bad" {
		return forge.CurrentUser{}, errors.New("secret must not appear")
	}
	return forge.CurrentUser{Login: "dries"}, c.err
}

func setup(t *testing.T) *Module {
	t.Helper()
	db, err := state.Open(statetest.Path(t), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(Deps{DB: db, Discover: func() []Discovered {
		return []Discovered{{Connection: Connection{Host: "github.com", Provider: forge.RemoteTypeGitHub, Source: "GitHub CLI"}, Client: fakeClient{token: "cli-secret"}}}
	}, Build: func(_ string, _ forge.RemoteType, token string) forge.Forge { return fakeClient{token: token} }})
}
func request(t *testing.T, m *Module, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	if w.Code != want {
		t.Fatalf("%s %d: %s", path, w.Code, w.Body.String())
	}
	return w
}
func TestCredentialSetup(t *testing.T) {
	m := setup(t)
	rem := forge.Remote{Host: "github.com", Type: forge.RemoteTypeGitHub}
	c, ok := m.Resolve(t.Context(), rem)
	if len(m.Clients(t.Context())) != 1 {
		t.Fatal("missing server credential snapshot")
	}
	if !ok || c.(fakeClient).token != "cli-secret" {
		t.Fatal("missing CLI fallback")
	}
	w := request(t, m, "GET", "/api/source-control", "", 200)
	if strings.Contains(w.Body.String(), "cli-secret") || !strings.Contains(w.Body.String(), "dries") {
		t.Fatal(w.Body.String())
	}
	request(t, m, "POST", "/api/source-control/credentials", `{"host":"github.com","provider":"github","token":"saved-secret"}`, 204)
	c, ok = m.Resolve(t.Context(), rem)
	if !ok || c.(fakeClient).token != "saved-secret" {
		t.Fatal("saved token not preferred")
	}
	w = request(t, m, "GET", "/api/source-control", "", 200)
	if strings.Contains(w.Body.String(), "saved-secret") || !strings.Contains(w.Body.String(), "saved token") {
		t.Fatal(w.Body.String())
	}
	// Reload durable overrides with a new module.
	m = New(m.d)
	c, ok = m.Resolve(t.Context(), rem)
	if !ok || c.(fakeClient).token != "saved-secret" {
		t.Fatal("saved token not restored")
	}
	request(t, m, "POST", "/api/source-control/credentials", `{"host":"github.com","provider":"github","token":""}`, 204)
	c, ok = m.Resolve(t.Context(), rem)
	if !ok || c.(fakeClient).token != "cli-secret" {
		t.Fatal("fallback not restored")
	}
	request(t, m, "POST", "/api/source-control/credentials", `{"host":"code.example.com","provider":"forgejo","token":"other-secret"}`, 204)
	if !m.Knows("code.example.com") {
		t.Fatal("new Forgejo host not classified")
	}
	if _, ok := m.Resolve(t.Context(), forge.Remote{Host: "code.example.com", Type: forge.RemoteTypeGitHub}); ok {
		t.Fatal("provider mismatch accepted")
	}
	m.d.Rediscover = func() []Discovered {
		return []Discovered{{Connection: Connection{Host: "github.com", Provider: forge.RemoteTypeGitHub, Source: "GH_TOKEN"}, Client: fakeClient{token: "new-env"}}}
	}
	request(t, m, "POST", "/api/source-control/refresh", `{}`, 204)
	c, _ = m.Resolve(t.Context(), rem)
	if c.(fakeClient).token != "new-env" {
		t.Fatal("recheck did not rediscover")
	}
}

func TestCredentialFailures(t *testing.T) {
	m := setup(t)
	for _, body := range []string{`{`, `{"host":"https://evil","provider":"forgejo","token":"a"}`, `{"host":"github.com/evil","provider":"github","token":"a"}`, `{"host":"evil","provider":"unknown","token":"a"}`, `{"host":"github.com","provider":"github","token":"bad"}`, `{"host":"github.com","provider":"github","token":"a\nb"}`} {
		request(t, m, "POST", "/api/source-control/credentials", body, 400)
	}
	m.d.Discover = func() []Discovered {
		return []Discovered{{Connection: Connection{Host: "github.com", Provider: forge.RemoteTypeGitHub}, Client: fakeClient{err: forge.ErrUnauthenticated}}}
	}
	m.loaded = false
	w := request(t, m, "GET", "/api/source-control", "", 200)
	if !strings.Contains(w.Body.String(), "not authenticated") {
		t.Fatal(w.Body.String())
	}
	m.d.Discover = func() []Discovered {
		return []Discovered{{Connection: Connection{Host: "github.com", Provider: forge.RemoteTypeGitHub}, Client: fakeClient{err: errors.New("network")}}}
	}
	m.loaded = false
	w = request(t, m, "GET", "/api/source-control", "", 200)
	if !strings.Contains(w.Body.String(), "unavailable") {
		t.Fatal(w.Body.String())
	}
	if err := m.d.DB.Close(); err != nil {
		t.Fatal(err)
	}
	m.loaded = false
	request(t, m, "GET", "/api/source-control", "", 503)
	if _, ok := m.Resolve(t.Context(), forge.Remote{Host: "github.com", Type: forge.RemoteTypeGitHub}); ok {
		t.Fatal("store failure fell back")
	}
	n := New(Deps{})
	request(t, n, "POST", "/api/source-control/credentials", `{"host":"github.com","provider":"github","token":"a"}`, 503)
	request(t, n, "GET", "/api/source-control", "", 200)
}

func TestHostValidationAndClients(t *testing.T) {
	for _, host := range []string{"", "x@evil.com", "evil.com/path", "evil.com?x", "evil.com#x", "evil.com\\path", "evil.com\n"} {
		if validHost(host, forge.RemoteTypeForgejo) {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"code.example.com", "code.example.com:3000"} {
		if !validHost(host, forge.RemoteTypeForgejo) {
			t.Fatal(host)
		}
	}
	if buildClient("github.com", forge.RemoteTypeGitHub, "s") == nil || buildClient("code.example.com", forge.RemoteTypeForgejo, "s") == nil {
		t.Fatal("nil client")
	}
}
