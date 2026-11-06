package claudeprovider

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestContextTokensUsesSessionRecencyIndex(t *testing.T) {
	x, err := OpenIndex(filepath.Join(t.TempDir(), "claude.db"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	rows, err := x.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+contextTokensSQL, "s")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "usage_latest (session_id=?)") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}

func TestContextIndexUpgradePreservesUsage(t *testing.T) {
	path, config := filepath.Join(t.TempDir(), "claude.db"), t.TempDir()
	x, err := OpenIndex(path, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(`DROP INDEX usage_latest;
		INSERT INTO usage VALUES
		('s','a',100,1,'{"role":"assistant","tokens":{"input":10,"output":2}}','[]',1),
		('s','b',100,1,'{"role":"assistant","tokens":{"input":20,"output":3}}','[]',1),
		('s','c',101,1,'{"role":"user","tokens":{"output":9}}','[]',1),
		('s','d',102,0,'{"role":"assistant","tokens":{"output":0}}','[]',1),
		('other','e',103,1,'{"role":"assistant","tokens":{"output":999}}','[]',1)`); err != nil {
		t.Fatal(err)
	}
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	x, err = OpenIndex(path, config)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if tokens, err := x.contextTokens(t.Context(), "s"); err != nil || tokens != 23 {
		t.Fatalf("latest complete assistant context = %d, %v", tokens, err)
	}
	var count int
	if err := x.db.QueryRow("SELECT count(*) FROM usage").Scan(&count); err != nil || count != 5 {
		t.Fatalf("upgrade reset usage: %d, %v", count, err)
	}
}
