package platforms

import "testing"

func TestModelReferences(t *testing.T) {
	for ref, want := range map[string]struct {
		platform ID
		model    string
		selects  ID
	}{
		"":                                   {"", "", ""},
		"anthropic/claude-sonnet-4-5":        {"", "anthropic/claude-sonnet-4-5", OpenCodeID},
		"claudecode:anthropic/sonnet":        {"claudecode", "anthropic/sonnet", "claudecode"},
		"openrouter/meta-llama/llama-3:free": {"", "openrouter/meta-llama/llama-3:free", OpenCodeID},
		"sonnet":                             {"", "sonnet", OpenCodeID},
		":anthropic/x":                       {"", ":anthropic/x", OpenCodeID},
	} {
		platform, model := SplitModel(ref)
		if platform != want.platform || model != want.model || ModelPlatform(ref) != want.selects {
			t.Errorf("%q = %q %q selects %q", ref, platform, model, ModelPlatform(ref))
		}
		if ref != "" && ref[0] != ':' && QualifyModel(ModelPlatform(ref), model) != ref {
			t.Errorf("%q does not round-trip: %q", ref, QualifyModel(ModelPlatform(ref), model))
		}
	}
	if QualifyModel("claudecode", "") != "" || QualifyModel("", "a/b") != "a/b" {
		t.Error("empty qualify")
	}
	if m, err := ModelFor("claudecode", "claudecode:anthropic/sonnet"); err != nil || m != "anthropic/sonnet" {
		t.Errorf("own prefix = %q %v", m, err)
	}
	if m, err := ModelFor("claudecode", "anthropic/sonnet"); err != nil || m != "anthropic/sonnet" {
		t.Errorf("unprefixed = %q %v", m, err)
	}
	if _, err := ModelFor(OpenCodeID, "claudecode:anthropic/sonnet"); err == nil {
		t.Error("foreign prefix accepted")
	}
}

func TestPlatformForModel(t *testing.T) {
	for _, tc := range []struct{ requested, model, want string }{
		{"", "", ""},
		{"r-box:claudecode", "", "r-box:claudecode"},
		{"", "anthropic/claude", "opencode"},
		{"", "claudecode:anthropic/sonnet", "claudecode"},
		{"r-box:claudecode", "claudecode:anthropic/sonnet", "r-box:claudecode"},
		{"claudecode", "anthropic/claude", "claudecode"}, // unprefixed: the named platform's own
	} {
		if got, err := PlatformForModel(tc.requested, tc.model); err != nil || got != tc.want {
			t.Errorf("%q + %q = %q %v, want %q", tc.requested, tc.model, got, err, tc.want)
		}
	}
	for _, tc := range [][2]string{{"opencode", "claudecode:anthropic/sonnet"}, {"r-box:opencode", "claudecode:a/b"}} {
		if _, err := PlatformForModel(tc[0], tc[1]); err == nil {
			t.Errorf("%q + %q accepted", tc[0], tc[1])
		}
	}
	if ID("r-box:claudecode").Base() != "claudecode" || ID("opencode").Base() != "opencode" {
		t.Error("Base")
	}
}
