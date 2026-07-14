package workflows

import "testing"

func TestNormalizeLeasePath(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "src/app", "src/app", false},
		{"leading dot", "./src/app", "src/app", false},
		{"trailing slash", "src/app/", "src/app", false},
		{"backslashes", `src\app`, "src/app", false},
		{"redundant", "src//app/./mod", "src/app/mod", false},
		{"interior dotdot resolves", "src/app/../lib", "src/lib", false},
		{"empty", "", "", true},
		{"absolute", "/etc/passwd", "", true},
		{"shard root", ".", "", true},
		{"escapes", "../secret", "", true},
		{"escapes deep", "src/../../secret", "", true},
		{"nul", "src/a\x00b", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLeasePath(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLeasePathsOverlap(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"exact", "src/app", "src/app", true},
		{"ancestor", "src", "src/app", true},
		{"descendant", "src/app/mod", "src/app", true},
		{"disjoint siblings", "src/a", "src/b", false},
		{"prefix string not ancestor", "src/a", "src/ab", false},
		{"unrelated", "docs", "src", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leasePathsOverlap(tt.a, tt.b); got != tt.want {
				t.Fatalf("overlap(%q,%q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := leasePathsOverlap(tt.b, tt.a); got != tt.want {
				t.Fatalf("overlap is not symmetric for %q,%q", tt.a, tt.b)
			}
		})
	}
}

func TestNormalizedLeasePathsRejectsSelfOverlap(t *testing.T) {
	if _, err := normalizedLeasePaths([]string{"src", "src/app"}); err == nil {
		t.Fatal("expected ancestor self-overlap to be rejected")
	}
	got, err := normalizedLeasePaths([]string{"./src/a/", `src\b`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "src/a" || got[1] != "src/b" {
		t.Fatalf("normalized disjoint scopes = %v", got)
	}
}

func TestGitMutationDenied(t *testing.T) {
	restricted := defaultRestrictedGitSubcommands
	tests := []struct {
		name    string
		command []string
		want    bool
	}{
		{"commit", []string{"git", "commit", "-m", "x"}, true},
		{"push", []string{"git", "push", "origin", "main"}, true},
		{"stash", []string{"git", "stash"}, true},
		{"reset hard", []string{"git", "reset", "--hard"}, true},
		{"checkout", []string{"git", "checkout", "main"}, true},
		{"commit past -C", []string{"git", "-C", "/repo", "commit"}, true},
		{"commit past -c config", []string{"git", "-c", "user.name=x", "commit"}, true},
		{"restore", []string{"git", "restore", "file"}, true},
		{"shell wrapped commit", []string{"/bin/sh", "-c", "git commit -m done"}, true},
		{"bash wrapped push", []string{"/bin/bash", "-c", "git push"}, true},
		{"diff allowed", []string{"git", "diff"}, false},
		{"status allowed", []string{"git", "status"}, false},
		{"log allowed", []string{"git", "-C", "/repo", "log"}, false},
		{"not git", []string{"/usr/bin/printf", "commit"}, false},
		{"shell with operators not unwrapped", []string{"/bin/sh", "-c", "echo hi && git commit"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitMutationDenied(tt.command, restricted); got != tt.want {
				t.Fatalf("gitMutationDenied(%v) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestScopesConflict(t *testing.T) {
	if !scopesConflict([]string{"src/a", "docs"}, []string{"README", "src/a/x"}) {
		t.Fatal("expected conflict via ancestor src/a vs src/a/x")
	}
	if scopesConflict([]string{"src/a", "docs"}, []string{"src/b", "lib"}) {
		t.Fatal("disjoint scope sets must not conflict")
	}
}
