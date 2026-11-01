package filesystem

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// legacyFileToken mints the pre-expiry token shape: HMAC over the path only.
func legacyFileToken(key []byte, path string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(path))
	return base64.RawURLEncoding.EncodeToString([]byte(path)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func writeFixturePNG(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(p, []byte("\x89PNG\r\n\x1a\nfake"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A URL minted before expiries existed carries no lifetime, so the proxy
// must not serve it any more; only the dashboard can re-mint it.
func TestHandleFileProxyRejectsTokenWithoutExpiry(t *testing.T) {
	m := newFileTestModule(t)
	png := writeFixturePNG(t)
	key, err := m.fileTokenSecret(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	m.handleFileProxy(rec, httptest.NewRequest(http.MethodGet, filePathPrefix+legacyFileToken(key, png), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("legacy token status = %d, want 403", rec.Code)
	}
}

// FileURL embeds an expiry about seven days ahead, visible in the URL.
func TestFileURLCarriesExpiry(t *testing.T) {
	m := newFileTestModule(t)
	link, err := m.FileURL(t.Context(), "/tmp/chart.png")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimPrefix(link, filePathPrefix), ".")
	if len(parts) != 3 {
		t.Fatalf("token %q has %d parts, want <path>.<expiry>.<mac>", link, len(parts))
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		t.Fatalf("expiry %q: %v", parts[1], err)
	}
	if d := time.Until(time.Unix(exp, 0)); d < 6*24*time.Hour || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("expiry is %s away, want about seven days", d)
	}
}

func testSign(key []byte, path string) string {
	return signFilePath(key, path, fileURLExpiry(time.Now()))
}

func testVerify(key []byte, token string) (string, bool) {
	return verifyFileToken(key, token, time.Now())
}

func TestVerifyFileTokenExpiry(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Unix(1_800_000_000, 0)
	exp := fileURLExpiry(now)
	tok := signFilePath(key, "/tmp/a.png", exp)
	parts := strings.Split(tok, ".")
	tests := []struct {
		name  string
		token string
		at    time.Time
		ok    bool
	}{
		{"fresh", tok, now, true},
		{"last second", tok, time.Unix(exp-1, 0), true},
		{"expired", tok, time.Unix(exp, 0), false},
		{"lifetime beyond TTL", signFilePath(key, "/tmp/a.png", now.Add(fileURLTTL+time.Hour).Unix()), now, false},
		{"non-canonical expiry", parts[0] + ".+" + parts[1] + "." + parts[2], now, false},
		{"zero expiry", signFilePath(key, "/tmp/a.png", 0), now, false},
		{"legacy", legacyFileToken(key, "/tmp/a.png"), now, false},
		{"bad expiry", parts[0] + ".x." + parts[2], now, false},
		{"bad signature encoding", parts[0] + "." + parts[1] + ".!!!", now, false},
		{"too many parts", tok + ".x", now, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := verifyFileToken(key, tt.token, tt.at)
			if ok != tt.ok || (ok && got != "/tmp/a.png") {
				t.Fatalf("verifyFileToken = %q, %v; want ok=%v", got, ok, tt.ok)
			}
		})
	}
}

// Mints made the same day are identical, so browser caches and gallery
// links survive reloads; the lifetime stays between six and seven days.
func TestFileURLExpiryStableWithinADay(t *testing.T) {
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if fileURLExpiry(day.Add(time.Minute)) != fileURLExpiry(day.Add(23*time.Hour)) {
		t.Fatal("expiry changed within one day")
	}
	for _, at := range []time.Time{day, day.Add(23*time.Hour + 59*time.Minute)} {
		d := time.Unix(fileURLExpiry(at), 0).Sub(at)
		if d <= 6*24*time.Hour-time.Minute || d > fileURLTTL {
			t.Fatalf("lifetime at %s = %s", at, d)
		}
	}
}

func TestHandleFileProxyRejectsExpiredURL(t *testing.T) {
	m := newFileTestModule(t)
	png := writeFixturePNG(t)
	link, err := m.FileURL(t.Context(), png)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	m.handleFileProxy(rec, httptest.NewRequest(http.MethodGet, link, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fresh status = %d", rec.Code)
	}
	m.now = func() time.Time { return time.Now().Add(fileURLTTL) }
	rec = httptest.NewRecorder()
	m.handleFileProxy(rec, httptest.NewRequest(http.MethodGet, link, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expired status = %d, want 403", rec.Code)
	}
}

func TestHandleFileURLRefresh(t *testing.T) {
	m := newFileTestModule(t)
	png := writeFixturePNG(t)
	key, err := m.fileTokenSecret(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expired := filePathPrefix + signFilePath(key, png, time.Now().Add(-time.Hour).Unix())
	refresh := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		m.handleFileURLRefresh(rec, httptest.NewRequest(http.MethodPost, fileRefreshPath, strings.NewReader(body)))
		return rec
	}

	for name, old := range map[string]string{
		"expired": expired,
		"legacy":  filePathPrefix + legacyFileToken(key, png),
	} {
		t.Run(name, func(t *testing.T) {
			rec := refresh(`{"url":"` + old + `"}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var resp struct{ URL string }
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if got, ok := verifyFileToken(key, strings.TrimPrefix(resp.URL, filePathPrefix), time.Now()); !ok || got != png {
				t.Fatalf("refreshed URL %q does not serve %s", resp.URL, png)
			}
			served := httptest.NewRecorder()
			m.handleFileProxy(served, httptest.NewRequest(http.MethodGet, resp.URL, nil))
			if served.Code != http.StatusOK {
				t.Fatalf("refreshed URL status = %d", served.Code)
			}
		})
	}

	tampered := expired[:len(expired)-2] + "AA"
	for name, body := range map[string]string{
		"tampered":     `{"url":"` + tampered + `"}`,
		"other route":  `{"url":"/api/git/file"}`,
		"other key":    `{"url":"` + filePathPrefix + signFilePath([]byte("fedcba9876543210fedcba9876543210"), png, fileURLExpiry(time.Now())) + `"}`,
		"absolute URL": `{"url":"http://evil.example` + expired + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := refresh(body); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
	if rec := refresh("{"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400", rec.Code)
	}
}

func TestHandleFileURLRefreshWithoutStateDB(t *testing.T) {
	rec := httptest.NewRecorder()
	NewModule(Deps{}).handleFileURLRefresh(rec, httptest.NewRequest(http.MethodPost, fileRefreshPath, strings.NewReader(`{"url":"/api/file/x"}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
