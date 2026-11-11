package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/relay"
	"github.com/ocman/ocman/internal/relayauth"
	"github.com/ocman/ocman/internal/share"
)

type ssoIdentityTransport struct{}

func (ssoIdentityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{"access_token":"provider-access"}`
	if r.URL.Host == "api.github.com" {
		body = `{"id":123}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// Exercise both real services. Only the external identity provider is faked.
func TestRelaySSOConnectionCreatesRealInbox(t *testing.T) {
	remote := httptest.NewUnstartedServer(nil)
	t.Cleanup(remote.Close)
	base := "http://" + remote.Listener.Addr().String()
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth, err := relayauth.New(relayauth.Config{
		PublicURL: base, Store: store, HTTP: &http.Client{Transport: ssoIdentityTransport{}},
		Providers: []relayauth.Provider{{ID: "github", Name: "GitHub", Kind: "github", ClientID: "client", ClientSecret: "secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := relay.New(relay.Config{Store: store, EnrollmentAuth: auth})
	if err != nil {
		t.Fatal(err)
	}
	remote.Config.Handler = server
	remote.Start()
	m, owner := testModule(t)
	relayURL = base
	w := do(t, owner, http.MethodPost, "/api/settings/webhook-relay/connect", "")
	if w.Code != http.StatusOK || m.connection.pollToken == "" || strings.Contains(w.Body.String(), m.connection.pollToken) {
		t.Fatalf("private connection: %d %s", w.Code, w.Body)
	}
	var connect struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &connect); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(connect.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(u.Path, "/auth/connect/")
	browser := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	start := browser(http.MethodGet, "/auth/login/"+id+"/github", "", nil)
	if start.Code != http.StatusSeeOther {
		t.Fatalf("login: %d %s", start.Code, start.Body)
	}
	cookie := start.Result().Cookies()[0]
	callback := browser(http.MethodGet, "/auth/callback/github?state="+cookie.Value+"&code=code", "", cookie)
	if callback.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", callback.Code, callback.Body)
	}
	approval := browser(http.MethodPost, "/auth/approve", "state="+cookie.Value, cookie)
	if approval.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", approval.Code, approval.Body)
	}
	w = do(t, owner, http.MethodPost, "/api/settings/webhook-relay/connect/poll", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hasEnrollmentToken":true`) {
		t.Fatalf("poll: %d %s", w.Code, w.Body)
	}
	token, _, err := m.store.GetSetting(t.Context(), settingWebhookEnrollmentToken)
	if err != nil || !auth.Authorize(t.Context(), token) || strings.Contains(w.Body.String(), token) {
		t.Fatal("enrollment token not saved privately")
	}
	w = do(t, owner, http.MethodPost, "/api/webhook-inboxes", `{"name":"SSO inbox"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("real inbox registration: %d %s", w.Code, w.Body)
	}
}
