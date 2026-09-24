package db

import (
	"strings"
	"testing"
)

func TestSearchSessionText(t *testing.T) {
	d := openTestDB(t)
	insertSession(t, d, "recent", "Recent", "/repo", 1, 2000)
	insertSession(t, d, "other-dir", "Other", "/other", 1, 2000)
	insertSession(t, d, "old", "Old", "/repo", 1, 10)
	insertMessage(t, d, "m1", "recent", 1, map[string]any{"role": "user"})
	insertMessage(t, d, "m2", "recent", 2, map[string]any{"role": "assistant"})
	insertMessage(t, d, "m3", "other-dir", 1, map[string]any{"role": "user"})
	insertMessage(t, d, "m4", "old", 1, map[string]any{"role": "user"})
	insertPart(t, d, "p1", "m1", "recent", 1, map[string]any{"type": "text", "text": "please run Weave-CLI deploy"})
	insertPart(t, d, "p2", "m2", "recent", 2, map[string]any{"type": "tool", "state": map[string]any{"output": "weave-cli ok"}})
	insertPart(t, d, "p3", "m2", "recent", 3, map[string]any{"type": "text", "text": "nothing here"})
	insertPart(t, d, "p4", "m3", "other-dir", 1, map[string]any{"type": "text", "text": "weave-cli"})
	insertPart(t, d, "p5", "m4", "old", 1, map[string]any{"type": "text", "text": "weave-cli"})

	got, err := d.SearchSessionText(t.Context(), "weave-cli", "", 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, m := range got {
		ids = append(ids, m.SessionID+"/"+m.PartID+"/"+m.Role)
	}
	// Tool parts, non-matching text, and sessions outside the window are skipped.
	if strings.Join(ids, ",") != "recent/p1/user,other-dir/p4/user" && strings.Join(ids, ",") != "other-dir/p4/user,recent/p1/user" {
		t.Fatalf("matches = %v", ids)
	}

	got, err = d.SearchSessionText(t.Context(), "WEAVE-cli", "/repo", 1000, 10)
	if err != nil || len(got) != 1 || got[0].PartID != "p1" || got[0].Snippet != "please run Weave-CLI deploy" {
		t.Fatalf("directory-scoped matches = %#v, %v", got, err)
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("a", 100) + " héllo needle\nthere " + strings.Repeat("b", 100)
	got := snippet(long, "needle")
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "héllo needle there") {
		t.Fatalf("snippet = %q", got)
	}
	if got := snippet("short needle", "needle"); got != "short needle" {
		t.Fatalf("short snippet = %q", got)
	}
	// Cut points never split a multi-byte rune.
	if got := snippet(strings.Repeat("é", 100)+"x", "x"); !strings.HasPrefix(got, "…é") {
		t.Fatalf("rune-boundary snippet = %q", got)
	}
}
