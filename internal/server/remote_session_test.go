package server

import "testing"

func TestIsRemotePlatformID(t *testing.T) {
	cases := map[string]bool{
		"opencode":            false,
		"claude-code":         false,
		"r-abc:opencode":      true,
		"r-xyz123:opencode":   true,
		"r":                   false,
		"":                    false,
		"remote-but-not-pref": false,
	}
	for id, want := range cases {
		if got := isRemotePlatformID(id); got != want {
			t.Errorf("isRemotePlatformID(%q) = %v, want %v", id, got, want)
		}
	}
}
