package platforms

import "testing"

func TestSplitPlatformID(t *testing.T) {
	cases := []struct {
		in, wantRemote, wantBase string
	}{
		{"opencode", "", "opencode"},
		{"r-abc:opencode", "abc", "opencode"},
		{"r-abc123:claude-code", "abc123", "claude-code"},
		{"r-malformed", "", "r-malformed"},
	}
	for _, c := range cases {
		gotRemote, gotBase := SplitPlatformID(c.in)
		if gotRemote != c.wantRemote || gotBase != c.wantBase {
			t.Errorf("SplitPlatformID(%q) = (%q,%q), want (%q,%q)",
				c.in, gotRemote, gotBase, c.wantRemote, c.wantBase)
		}
	}
}

func TestCompoundPlatformID(t *testing.T) {
	if got := CompoundPlatformID("abc", "opencode"); got != "r-abc:opencode" {
		t.Errorf("CompoundPlatformID = %q", got)
	}
	// Round-trips with SplitPlatformID.
	id := CompoundPlatformID("xyz", "opencode")
	r, b := SplitPlatformID(id)
	if r != "xyz" || b != "opencode" {
		t.Errorf("round-trip mismatch: %q %q", r, b)
	}
}
