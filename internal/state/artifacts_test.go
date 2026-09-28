package state

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openArtifactDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func putBlob(t *testing.T, d *DB, content string) ArtifactItem {
	t.Helper()
	sum, size, err := d.PutArtifactBlob(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return ArtifactItem{Kind: ArtifactItemFile, Name: content + ".txt", MIME: "text/plain", Size: size, SHA256: sum}
}

func mustCreate(t *testing.T, d *DB, a Artifact) Artifact {
	t.Helper()
	got, err := d.CreateArtifact(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMigrateV101CreatesArtifactTables(t *testing.T) {
	d := openArtifactDB(t)
	for _, table := range []string{"artifact", "artifact_item", "artifact_share"} {
		var n int
		if err := d.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", table, n, err)
		}
	}
	if _, err := d.db.Exec(`INSERT INTO artifact (id,title,directory,created_at) VALUES ('a','t','/p',1);
		INSERT INTO artifact_share (id,artifact_id,relay_id,relay_key,relay_delete_token,relay_url,created_at) VALUES ('s','a','r','k','d','u',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO artifact_item (artifact_id,ordinal,kind) VALUES ('a',0,'bogus')`); err == nil {
		t.Fatal("kind check not enforced")
	}
}

func TestCreateArtifactValidation(t *testing.T) {
	d := openArtifactDB(t)
	file := putBlob(t, d, "x")
	link := ArtifactItem{Kind: ArtifactItemLink, URL: "https://example.com", Label: "ex"}
	cases := []struct {
		name string
		a    Artifact
		ok   bool
	}{
		{"file and link", Artifact{Title: "t", Directory: "/p", Items: []ArtifactItem{file, link}}, true},
		{"session pair", Artifact{Title: "t", Directory: "/p", Platform: "opencode", SessionID: "s", Items: []ArtifactItem{link}}, true},
		{"no title", Artifact{Directory: "/p", Items: []ArtifactItem{link}}, false},
		{"no directory", Artifact{Title: "t", Items: []ArtifactItem{link}}, false},
		{"no items", Artifact{Title: "t", Directory: "/p"}, false},
		{"half pair", Artifact{Title: "t", Directory: "/p", SessionID: "s", Items: []ArtifactItem{link}}, false},
		{"bad link scheme", Artifact{Title: "t", Directory: "/p", Items: []ArtifactItem{{Kind: ArtifactItemLink, URL: "javascript:alert(1)"}}}, false},
		{"missing blob", Artifact{Title: "t", Directory: "/p", Items: []ArtifactItem{{Kind: ArtifactItemFile, Name: "n", SHA256: strings.Repeat("a", 64)}}}, false},
		{"size mismatch", Artifact{Title: "t", Directory: "/p", Items: []ArtifactItem{{Kind: ArtifactItemFile, Name: "n", SHA256: file.SHA256, Size: 9}}}, false},
		{"bad kind", Artifact{Title: "t", Directory: "/p", Items: []ArtifactItem{{Kind: "x"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.CreateArtifact(context.Background(), tc.a)
			if !tc.ok {
				if !errors.Is(err, ErrArtifactInvalid) {
					t.Fatalf("err = %v, want invalid", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			back, err := d.GetArtifact(context.Background(), got.ID)
			if err != nil || back.RemoteID != "local" || len(back.Items) != len(tc.a.Items) || back.Items[0].Kind != tc.a.Items[0].Kind {
				t.Fatalf("get = %+v, %v", back, err)
			}
		})
	}
	if _, err := d.GetArtifact(context.Background(), "missing"); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestArtifactBlobDedupAndSizeCap(t *testing.T) {
	d := openArtifactDB(t)
	a, b := putBlob(t, d, "same"), putBlob(t, d, "same")
	if a.SHA256 != b.SHA256 || a.Size != 4 {
		t.Fatalf("dedup: %+v %+v", a, b)
	}
	dir := filepath.Join(d.dataDir, "artifacts", "blobs")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("blobs = %v, %v", entries, err)
	}
	info, _ := os.Stat(filepath.Join(dir, a.SHA256))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("blob perm = %v", info.Mode().Perm())
	}
	f, err := d.OpenArtifactBlob(a.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	_ = f.Close()
	if string(data) != "same" {
		t.Fatalf("content = %q", data)
	}
	if _, err := d.OpenArtifactBlob("../state.db"); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("traversal = %v", err)
	}

	for _, tc := range []struct {
		size int64
		err  error
	}{{MaxArtifactFileBytes, nil}, {MaxArtifactFileBytes + 1, ErrArtifactTooLarge}} {
		_, got, err := d.PutArtifactBlob(io.LimitReader(zeroReader{}, tc.size))
		if !errors.Is(err, tc.err) || (err == nil && got != tc.size) {
			t.Fatalf("size %d: got %d, %v", tc.size, got, err)
		}
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("rejected upload left files: %v", entries)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestDeleteArtifactKeepsSharedBlobs(t *testing.T) {
	d := openArtifactDB(t)
	ctx := context.Background()
	shared, own := putBlob(t, d, "shared"), putBlob(t, d, "own")
	first := mustCreate(t, d, Artifact{Title: "1", Directory: "/p", Items: []ArtifactItem{shared, own}})
	second := mustCreate(t, d, Artifact{Title: "2", Directory: "/p", Items: []ArtifactItem{shared}})
	if n, err := d.TotalArtifactBytes(ctx); err != nil || n != shared.Size+own.Size {
		t.Fatalf("total = %d, %v", n, err)
	}
	if n, err := d.CountArtifacts(ctx); err != nil || n != 2 {
		t.Fatalf("count = %d, %v", n, err)
	}
	if _, err := d.db.Exec(`INSERT INTO artifact_share (id,artifact_id,relay_id,relay_key,relay_delete_token,relay_url,created_at) VALUES ('s',?,'r','k','d','u',1)`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteArtifact(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.OpenArtifactBlob(own.SHA256); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("own blob survived: %v", err)
	}
	f, err := d.OpenArtifactBlob(shared.SHA256)
	if err != nil {
		t.Fatalf("shared blob removed: %v", err)
	}
	_ = f.Close()
	var shares int
	_ = d.db.QueryRow(`SELECT count(*) FROM artifact_share`).Scan(&shares)
	if shares != 0 {
		t.Fatalf("shares left = %d", shares)
	}
	if err := d.DeleteArtifact(ctx, first.ID); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if err := d.DeleteArtifact(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.OpenArtifactBlob(shared.SHA256); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("shared blob survived last delete: %v", err)
	}
	if n, _ := d.TotalArtifactBytes(ctx); n != 0 {
		t.Fatalf("total after delete = %d", n)
	}
}

func TestListArtifactsPaginationStable(t *testing.T) {
	d := openArtifactDB(t)
	ctx := context.Background()
	link := []ArtifactItem{{Kind: ArtifactItemLink, URL: "https://x.test"}}
	want := map[string]bool{}
	for range 7 {
		want[mustCreate(t, d, Artifact{Title: "a", Directory: "/p", Items: link}).ID] = true
	}
	seen := map[string]int{}
	cursor, pages := "", 0
	for {
		page, next, err := d.ListArtifacts(ctx, ArtifactFilter{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page {
			seen[a.ID]++
			if len(a.Items) != 1 {
				t.Fatalf("items not loaded: %+v", a)
			}
		}
		// Inserting mid-paging must not shift the remaining pages.
		mustCreate(t, d, Artifact{Title: "late", Directory: "/p", Items: link})
		pages++
		if next == "" {
			break
		}
		cursor = next
	}
	for id := range want {
		if seen[id] != 1 {
			t.Fatalf("artifact %s seen %d times (pages=%d)", id, seen[id], pages)
		}
	}
	if _, _, err := d.ListArtifacts(ctx, ArtifactFilter{Cursor: "!!"}); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("bad cursor = %v", err)
	}
	all, _, _ := d.ListArtifacts(ctx, ArtifactFilter{Limit: 1000})
	if len(all) != 7+pages {
		t.Fatalf("limit clamp/total = %d", len(all))
	}
}

func TestListArtifactsFilters(t *testing.T) {
	d := openArtifactDB(t)
	ctx := context.Background()
	file := putBlob(t, d, "report")
	link := ArtifactItem{Kind: ArtifactItemLink, URL: "https://x.test"}
	mustCreate(t, d, Artifact{Title: "Alpha", Directory: "/p1", Platform: "opencode", SessionID: "root", Items: []ArtifactItem{link}})
	mustCreate(t, d, Artifact{Title: "Beta", Description: "50% done", Directory: "/p1", Platform: "opencode", SessionID: "child", Items: []ArtifactItem{link}})
	mustCreate(t, d, Artifact{Title: "Gamma", Directory: "/p2", Items: []ArtifactItem{file}})
	cases := []struct {
		name string
		f    ArtifactFilter
		want []string
	}{
		{"all", ArtifactFilter{}, []string{"Alpha", "Beta", "Gamma"}},
		{"directory", ArtifactFilter{Directory: "/p1"}, []string{"Alpha", "Beta"}},
		{"session", ArtifactFilter{Platform: "opencode", SessionIDs: []string{"root"}}, []string{"Alpha"}},
		{"descendants", ArtifactFilter{Platform: "opencode", SessionIDs: []string{"root", "child"}}, []string{"Alpha", "Beta"}},
		{"q title", ArtifactFilter{Q: "alp"}, []string{"Alpha"}},
		{"q item name", ArtifactFilter{Q: "report.txt"}, []string{"Gamma"}},
		{"q escapes percent", ArtifactFilter{Q: "50%"}, []string{"Beta"}},
		{"q wildcard literal", ArtifactFilter{Q: "%"}, []string{"Beta"}},
		{"combined", ArtifactFilter{Directory: "/p2", Q: "alpha"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, next, err := d.ListArtifacts(ctx, tc.f)
			if err != nil || next != "" {
				t.Fatalf("err=%v next=%q", err, next)
			}
			var titles []string
			for _, a := range got {
				titles = append(titles, a.Title)
			}
			slices.Sort(titles) // same-millisecond rows order by id
			if strings.Join(titles, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", titles, tc.want)
			}
		})
	}
	if _, _, err := d.ListArtifacts(ctx, ArtifactFilter{SessionIDs: []string{"root"}}); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("session without platform = %v", err)
	}
}
