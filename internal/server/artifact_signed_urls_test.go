package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestArtifactMCPSignedURLs(t *testing.T) {
	srv, request := artifactServer(t)
	srv.auth = &Auth{hmacKey: []byte(strings.Repeat("k", 32)), cookieName: authCookieName}
	srv.publicBaseURL = "https://ocman.example"
	svc := artifactMCPService{srv}
	a, err := svc.CreateArtifact(t.Context(), internalmcp.ArtifactCreateRequest{
		Title: "Report", Directory: "/repo",
		Files: []internalmcp.ArtifactFile{{Name: "a.md", Content: "# hi"}, {Name: "b.md", Content: "secret"}},
		Links: []internalmcp.ArtifactLink{{URL: "https://example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetArtifact(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	list, _, err := svc.ListArtifacts(t.Context(), state.ArtifactFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	for _, view := range []state.Artifact{a, got, list[0]} {
		u, err := url.Parse(view.Items[0].URL)
		if err != nil || u.Host != "ocman.example" || u.Query().Get("token") == "" {
			t.Fatalf("expected absolute signed URL, got %q", view.Items[0].URL)
		}
		rec := request(http.MethodGet, u.RequestURI())
		if rec.Code != http.StatusOK || rec.Body.String() != "# hi" {
			t.Fatalf("signed fetch = %d, %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("signed response headers = %v", rec.Header())
		}
		if view.Items[2].URL != "https://example.com" {
			t.Fatal("external link changed")
		}
	}
	u, _ := url.Parse(got.Items[0].URL)
	for name, target := range map[string]string{
		"unsigned":         ArtifactFilePath(a.ID, 0),
		"tampered token":   u.RequestURI() + "x",
		"other file":       ArtifactFilePath(a.ID, 1) + "?" + u.RawQuery,
		"other artifact":   ArtifactFilePath("other", 0) + "?" + u.RawQuery,
		"metadata":         "/api/artifacts/" + a.ID + "?" + u.RawQuery,
		"list":             "/api/artifacts?" + u.RawQuery,
		"interactive":      u.Path + "/interactive?" + u.RawQuery,
		"expired":          u.Path + "?token=" + srv.auth.artifactURLToken(u.Path, time.Now().Add(-time.Second)),
		"cookie signature": u.Path + "?token=" + srv.auth.signToken(time.Now().Add(time.Minute)),
	} {
		if rec := request(http.MethodGet, target); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, rec.Code)
		}
	}
	if rec := request(http.MethodPost, u.RequestURI()); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
	for _, target := range []string{u.Path, u.Path + "?token=invalid"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: srv.auth.signToken(time.Now().Add(time.Hour))})
		rec := httptest.NewRecorder()
		srv.handleArtifacts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("browser cookie fetch = %d", rec.Code)
		}
	}
	stored, err := srv.stateDB.GetArtifact(t.Context(), a.ID)
	if err != nil || stored.Items[0].URL != "" {
		t.Fatalf("signed URL persisted: %v, %v", stored, err)
	}
	if err := srv.stateDB.DeleteArtifact(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if rec := request(http.MethodGet, u.RequestURI()); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted artifact = %d", rec.Code)
	}
}

func TestArtifactURLTokenScopeAndExpiry(t *testing.T) {
	a := &Auth{hmacKey: []byte(strings.Repeat("k", 32))}
	now := time.Unix(1234567890, 0)
	path := ArtifactFilePath("one", 0)
	token := a.artifactURLToken(path, now.Add(artifactURLTTL))
	for _, tc := range []struct {
		name, path, token string
		now               time.Time
		want              bool
	}{
		{"valid", path, token, now, true},
		{"just before expiry", path, token, now.Add(artifactURLTTL - time.Second), true},
		{"expired", path, token, now.Add(artifactURLTTL), false},
		{"too far in future", path, a.artifactURLToken(path, now.Add(artifactURLTTL+time.Second)), now, false},
		{"other path", ArtifactFilePath("one", 1), token, now, false},
		{"cookie token", path, a.signToken(now.Add(artifactURLTTL)), now, false},
		{"malformed", path, "bad.signature", now, false},
		{"no signature", path, "1234567900", now, false},
		{"tampered", path, token + "x", now, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.validArtifactURLToken(tc.path, tc.token, tc.now); got != tc.want {
				t.Fatalf("valid = %v, want %v", got, tc.want)
			}
		})
	}
	other := &Auth{hmacKey: []byte(strings.Repeat("x", 32))}
	if other.validArtifactURLToken(path, token, now) {
		t.Fatal("different signing key accepted")
	}
}
