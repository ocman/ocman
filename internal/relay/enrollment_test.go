package relay

import (
	"context"
	"net/http"
	"testing"

	"filippo.io/age"
)

type fakeEnrollmentAuth struct{}

func (fakeEnrollmentAuth) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/providers", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}
func (fakeEnrollmentAuth) Authorize(_ context.Context, token string) bool {
	return token == "sso-token"
}

func TestSSOEnrollmentAuthorizesInboxRegistration(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.EnrollmentAuth = fakeEnrollmentAuth{} })
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"recipient":"` + identity.Recipient().String() + `"}`)
	if w := h.do(http.MethodPost, "/inboxes", body, "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatal("invalid enrollment accepted")
	}
	if w := h.do(http.MethodPost, "/inboxes", body, "sso-token"); w.Code != http.StatusCreated {
		t.Fatalf("SSO enrollment: %d %s", w.Code, w.Body)
	}
	if w := h.do(http.MethodGet, "/auth/providers", nil, ""); w.Code != http.StatusOK {
		t.Fatal("SSO routes not mounted")
	}
}
