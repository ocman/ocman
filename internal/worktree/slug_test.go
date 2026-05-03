package worktree

import (
	"strings"
	"testing"
)

func TestSlugForBranch(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   string
	}{
		// Simple cases
		{"plain", "main", "main"},
		{"already-slug", "feature-login", "feature-login"},

		// Slashes
		{"single-slash", "feature/login", "feature-login"},
		{"multi-slash", "user/dries/wt-feature", "user-dries-wt-feature"},

		// Capitals
		{"capitals", "Feature/Login", "feature-login"},
		{"all-caps", "MAIN", "main"},

		// Allowed punctuation
		{"underscores", "feature_login", "feature_login"},
		{"dots", "v1.2.3", "v1.2.3"},

		// Disallowed chars stripped
		{"spaces", "feature login", "feature-login"},
		{"colons", "feature:login", "feature-login"},
		{"unicode", "féature/lögin", "f-ature-l-gin"},

		// Collapse runs
		{"collapse-dashes", "feature--login", "feature-login"},
		{"collapse-from-strip", "feat!!!ure", "feat-ure"},

		// Trim
		{"trim-leading-dash", "-feature", "feature"},
		{"trim-trailing-dash", "feature-", "feature"},
		{"trim-leading-dot", ".hidden", "hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlugForBranch(tt.branch)
			if got != tt.want {
				t.Errorf("SlugForBranch(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestSlugForBranch_EmptyAfterStrip(t *testing.T) {
	// A branch with only disallowed chars must produce a non-empty slug
	// (deterministic hash-based fallback) so we never hand git an empty
	// path component.
	got := SlugForBranch("!!!")
	if got == "" {
		t.Fatalf("SlugForBranch(!!!) returned empty slug")
	}
	if !strings.HasPrefix(got, "wt-") {
		t.Errorf("SlugForBranch(!!!) = %q, want wt-<hash> fallback", got)
	}
	if len(got) != len("wt-")+8 {
		t.Errorf("SlugForBranch(!!!) = %q, want length %d", got, len("wt-")+8)
	}
}

func TestSlugForBranch_VeryLong(t *testing.T) {
	// Branches longer than the cap get truncated with a hash suffix to
	// keep collisions rare while the path stays manageable.
	long := strings.Repeat("a", 200)
	got := SlugForBranch(long)
	if len(got) > 96 {
		t.Errorf("SlugForBranch(<200 a>) length = %d, want <= 96", len(got))
	}
	// Must end with an 8-char hash to disambiguate two long names that
	// happen to share the truncated prefix.
	if !hasHashSuffix(got, 8) {
		t.Errorf("SlugForBranch(<200 a>) = %q, want -<8-char hash> suffix", got)
	}
}

func TestSlugForBranch_Deterministic(t *testing.T) {
	// Must be a pure function — same input, same output, every time.
	for i := 0; i < 5; i++ {
		if SlugForBranch("feature/login") != "feature-login" {
			t.Fatalf("SlugForBranch is not deterministic")
		}
	}
}

// hasHashSuffix checks that s ends with "-<n hex chars>".
func hasHashSuffix(s string, n int) bool {
	if len(s) < n+1 {
		return false
	}
	tail := s[len(s)-n-1:]
	if tail[0] != '-' {
		return false
	}
	for _, c := range tail[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
