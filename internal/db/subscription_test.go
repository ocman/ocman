package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

const credentialSchema = `CREATE TABLE credential (
	id TEXT PRIMARY KEY, integration_id TEXT, value TEXT, active INTEGER, time_created INTEGER
);`

func credentialDB(t *testing.T, seed string) (*DB, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Exec(credentialSchema + seed); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, w
}

func TestSubscriptionCredentialsSelection(t *testing.T) {
	for _, tt := range []struct {
		name, seed, token, account string
	}{
		{"active before newest", `
			('a','openai','{"type":"oauth","access":"selected","metadata":{"accountID":"account"}}',1,1),
			('b','openai','{"type":"oauth","access":"wrong"}',0,2)`, "selected", "account"},
		{"legacy newest", `
			('a','openai','{"type":"oauth","access":"wrong"}',NULL,1),
			('b','openai','{"type":"oauth","access":"selected","accountId":"legacy"}',NULL,2)`, "selected", "legacy"},
		{"legacy id tiebreak", `
			('a','openai','{"type":"oauth","access":"wrong"}',0,1),
			('b','openai','{"type":"oauth","access":"selected"}',0,1)`, "selected", ""},
		{"key does not select inactive oauth", `
			('a','openai','{"type":"key","key":"private"}',1,1),
			('b','openai','{"type":"oauth","access":"wrong"}',0,2)`, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := credentialDB(t, "INSERT INTO credential VALUES "+tt.seed+";")
			got, err := d.SubscriptionCredentials(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got["openai"].Access != tt.token || got["openai"].AccountID != tt.account {
				t.Fatal("did not select the expected account")
			}
		})
	}
}

func TestSubscriptionCredentialsRereadAndReadonly(t *testing.T) {
	d, w := credentialDB(t, `INSERT INTO credential VALUES
		('a','anthropic','{"type":"oauth","access":"first","expires":123}',1,1),
		('b','unrelated','not json',1,2);`)
	for _, token := range []string{"first", "refreshed"} {
		if _, err := w.Exec(`UPDATE credential SET value = json_set(value, '$.access', ?) WHERE id = 'a'`, token); err != nil {
			t.Fatal(err)
		}
		got, err := d.SubscriptionCredentials(t.Context())
		if err != nil || len(got) != 1 || got["anthropic"].Access != token || got["anthropic"].Expires != 123 {
			t.Fatal("did not reread the current credential")
		}
	}
	if _, err := d.db.Exec(`DELETE FROM credential`); err == nil {
		t.Fatal("credential reader allowed database writes")
	}
}

func TestSubscriptionCredentialsEmptyAndErrors(t *testing.T) {
	d, w := credentialDB(t, "")
	got, err := d.SubscriptionCredentials(t.Context())
	if err != nil || len(got) != 0 {
		t.Fatal("expected no credentials")
	}
	for _, value := range []any{`{"access":"secret",`, nil} {
		if _, err := w.Exec(`INSERT OR REPLACE INTO credential VALUES ('a','anthropic',?,1,1)`, value); err != nil {
			t.Fatal(err)
		}
		_, err := d.SubscriptionCredentials(t.Context())
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("expected sanitized credential error")
		}
	}
	if _, err := w.Exec(`DROP TABLE credential`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SubscriptionCredentials(t.Context()); err == nil {
		t.Fatal("missing credential table must fail")
	}
}
