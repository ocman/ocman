package providerprotocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestVersionNegotiation(t *testing.T) {
	for _, version := range []int{PreviousVersion, CurrentVersion} {
		if err := CheckVersion(version); err != nil {
			t.Fatal(err)
		}
	}
	var versionError *VersionError
	err := CheckVersion(CurrentVersion + 1)
	if !errors.As(err, &versionError) || versionError.Version != CurrentVersion+1 || !strings.Contains(err.Error(), "unsupported provider protocol version") {
		t.Fatal(err)
	}
}

func TestHelloWireCapabilities(t *testing.T) {
	var hello Hello
	err := json.Unmarshal([]byte(`{"protocolVersion":1,"providerId":"test","cliVersion":"1.2.3","capabilities":{"composer":true,"events":true,"nativeQueue":true,"rename":true,"account":true}}`), &hello)
	if err != nil {
		t.Fatal(err)
	}
	if hello.PlatformID != "test" || hello.CLIVersion != "1.2.3" || !hello.Capabilities.Composer || !hello.Capabilities.Events || !hello.Capabilities.NativeQueue || !hello.Capabilities.Rename || !hello.Capabilities.Account {
		t.Fatal(hello)
	}
	data, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"providerId":"test"`) || strings.Contains(string(data), `"platformId"`) {
		t.Fatalf("legacy handshake key changed: %s", data)
	}
	var copy Hello
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	if copy != hello {
		t.Fatal("handshake changed in round trip")
	}
}

func TestMatchPattern(t *testing.T) {
	for _, tt := range []struct {
		pattern, s string
		want       bool
	}{
		{`a \*`, "a *", true},
		{`a \*`, "a", false}, // an escaped trailing star stays literal
		{`a\b`, `a\b`, true}, // a lone backslash is literal
		{`cat \*`, "cat /etc/shadow", false},
		{`rm foo\?`, "rm foo/x", false},
		{"git *", "git", true},
		{"git *", "git status", true},
		{"", "anything", true},
		{"?", "ab", false},
	} {
		if got := MatchPattern(tt.pattern, tt.s); got != tt.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

func TestExactPatternRoundTrip(t *testing.T) {
	for in, want := range map[string]string{
		"echo *":           `echo \*`,
		"https://x/?a=1":   `https://x/\?a=1`,
		`grep "a\|b"`:      `grep "a\|b"`,
		`a\*`:              `a\\\*`,
		`a\\`:              `a\\\`,
		`trailing\`:        `trailing\`,
		"plain command -x": "plain command -x",
	} {
		if got := ExactPattern(in); got != want {
			t.Errorf("ExactPattern(%q) = %q, want %q", in, got, want)
		}
		if back := PatternValue(ExactPattern(in)); back != in {
			t.Errorf("PatternValue(ExactPattern(%q)) = %q", in, back)
		}
	}
}
