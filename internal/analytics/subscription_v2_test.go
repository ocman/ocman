package analytics

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
)

func TestSubscriptionUsageV2Credentials(t *testing.T) {
	ocv2.SetInstalledV2(true)
	t.Cleanup(func() { ocv2.SetInstalledV2(false) })
	path := filepath.Join(t.TempDir(), "opencode.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Exec(`CREATE TABLE credential (
		id TEXT PRIMARY KEY, integration_id TEXT, value TEXT, active INTEGER, time_created INTEGER
	);
	INSERT INTO credential VALUES
		('old', 'anthropic', '{"type":"oauth","access":"old-secret","expires":1}', 0, 1),
		('selected', 'anthropic', '{"type":"oauth","access":"current-secret","expires":4102444800000}', 1, 2),
		('newer', 'anthropic', '{"type":"oauth","access":"wrong-secret","expires":4102444800000}', 0, 3);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer current-secret" {
			t.Error("usage request did not use the active v2 credential")
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":12},"seven_day":{"utilization":34}}`))
	}))
	defer upstream.Close()
	m := newTestModule(Deps{SubscriptionCredentials: database.SubscriptionCredentials})
	m.subscription.authPath = filepath.Join(t.TempDir(), "auth.json")
	m.subscription.anthropicURL = upstream.URL
	for _, legacy := range []string{"", `{"anthropic":{"type":"oauth","access":"stale-secret","expires":1}}`} {
		if legacy != "" {
			if err := os.WriteFile(m.subscription.authPath, []byte(legacy), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		m.subscriptionCache = subscriptionUsageCache{}
		rec := httptest.NewRecorder()
		routes(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/provider-usage", nil))
		var got subscriptionUsageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusOK || len(got.Providers) != 1 || got.Providers[0].Status != "ok" || len(got.Providers[0].Windows) != 2 {
			t.Fatalf("v2 subscription usage missing: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret") {
			t.Fatal("response exposed credentials")
		}
	}
	// Removing the v2 account must not resurrect the stale legacy login.
	w, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, tt := range []struct {
		statement string
		status    int
	}{
		{`DELETE FROM credential`, http.StatusOK},
		{`DROP TABLE credential`, http.StatusInternalServerError},
	} {
		if _, err := w.Exec(tt.statement); err != nil {
			t.Fatal(err)
		}
		m.subscriptionCache = subscriptionUsageCache{}
		rec := httptest.NewRecorder()
		routes(m).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/provider-usage", nil))
		if rec.Code != tt.status || strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "expired") {
			t.Fatalf("unexpected v2 credential response: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if tt.status == http.StatusOK && strings.TrimSpace(rec.Body.String()) != `{"providers":[]}` {
			t.Fatal("removed v2 credential fell back to legacy credentials")
		}
	}
}
