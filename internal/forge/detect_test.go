package forge

import (
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/gitexec"
)

func TestClassifyRemotes(t *testing.T) {
	gh := func(name, repo string) Remote {
		return Remote{Name: name, Host: "github.com", Type: RemoteTypeGitHub, Repo: repo}
	}
	tests := []struct {
		name string
		in   []rawRemote
		want []Remote
	}{
		{
			name: "https github",
			in:   []rawRemote{{Name: "origin", URL: "https://github.com/alice/myproj.git"}},
			want: []Remote{gh("origin", "alice/myproj")},
		},
		{
			name: "ssh github",
			in:   []rawRemote{{Name: "origin", URL: "git@github.com:alice/myproj.git"}},
			want: []Remote{gh("origin", "alice/myproj")},
		},
		{
			// The raw URL is never exposed, so credentials cannot leak.
			name: "credentials are not exposed",
			in:   []rawRemote{{Name: "origin", URL: "https://user:secret@github.com/alice/myproj.git"}},
			want: []Remote{gh("origin", "alice/myproj")},
		},
		{
			name: "https forgejo",
			in:   []rawRemote{{Name: "internal", URL: "https://code.example.com/infra/myproj.git"}},
			want: []Remote{{Name: "internal", Host: "code.example.com", Type: RemoteTypeForgejo, Repo: "infra/myproj"}},
		},
		{
			name: "ssh forgejo",
			in:   []rawRemote{{Name: "internal", URL: "git@code.example.com:infra/myproj.git"}},
			want: []Remote{{Name: "internal", Host: "code.example.com", Type: RemoteTypeForgejo, Repo: "infra/myproj"}},
		},
		{
			// Detection runs on the owner, which does not know the hub's
			// Forgejo hosts: every non-GitHub host is typed Forgejo and the
			// hub narrows it.
			name: "non-github hosts are forgejo",
			in: []rawRemote{
				{Name: "gitlab", URL: "https://gitlab.com/x/y.git"},
				{Name: "origin", URL: "https://github.com/a/b.git"},
			},
			want: []Remote{
				{Name: "gitlab", Host: "gitlab.com", Type: RemoteTypeForgejo, Repo: "x/y"},
				gh("origin", "a/b"),
			},
		},
		{
			// `git remote -v` lists each remote twice (fetch + push).
			name: "deduplicates by remote name",
			in: []rawRemote{
				{Name: "origin", URL: "https://github.com/a/b.git"},
				{Name: "origin", URL: "https://github.com/a/b.git"},
			},
			want: []Remote{gh("origin", "a/b")},
		},
		{
			name: "no trailing .git",
			in:   []rawRemote{{Name: "origin", URL: "https://github.com/a/b"}},
			want: []Remote{gh("origin", "a/b")},
		},
		{
			// Sorted by remote name so the frontend renders groups
			// deterministically.
			name: "stable order",
			in: []rawRemote{
				{Name: "upstream", URL: "https://github.com/u/r.git"},
				{Name: "origin", URL: "https://github.com/o/r.git"},
				{Name: "fork", URL: "https://github.com/f/r.git"},
			},
			want: []Remote{gh("fork", "f/r"), gh("origin", "o/r"), gh("upstream", "u/r")},
		},
		{name: "rejects traversal", in: []rawRemote{{Name: "origin", URL: "https://github.com/../user/issues.git"}}},
		{name: "rejects extra path segment", in: []rawRemote{{Name: "origin", URL: "https://github.com/owner/repo/extra.git"}}},
		{name: "rejects encoded traversal", in: []rawRemote{{Name: "origin", URL: "https://github.com/owner/%2e%2e.git"}}},
		{name: "rejects missing repo path", in: []rawRemote{{Name: "origin", URL: "x:y@github.com"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRemotes(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

// SCP-style remotes must be split the way git does: the host is everything
// before the first ':' (minus userinfo), and a '/' before that ':' makes
// the remote a local path.
func TestParseRemoteURL_SCPStyleFollowsGit(t *testing.T) {
	tests := []struct {
		raw, host, repo string
		ok              bool
	}{
		{"git@github.com:alice/myproj.git", "github.com", "alice/myproj", true},
		{"github.com:alice/myproj.git", "github.com", "alice/myproj", true},
		{"me@corp@github.com:alice/myproj", "github.com", "alice/myproj", true},
		// git: ssh to host "a", path "b@github.com:o/r".
		{"a:b@github.com:o/r", "", "", false},
		// git: local paths, since a '/' precedes the first ':'.
		{"./x@github.com:o/r", "", "", false},
		{"/srv/git@github.com:o/r", "", "", false},
		{"../repo", "", "", false},
		{"git@:o/r", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			host, repo, ok := parseRemoteURL(tt.raw)
			if host != tt.host || repo != tt.repo || ok != tt.ok {
				t.Fatalf("parseRemoteURL(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.raw, host, repo, ok, tt.host, tt.repo, tt.ok)
			}
		})
	}
}

func TestParseGitRemoteV_Verbatim(t *testing.T) {
	// Verbatim output of `git remote -v`. Each remote line ends
	// with "(fetch)" or "(push)".
	in := `origin	https://github.com/alice/myproj.git (fetch)
origin	https://github.com/alice/myproj.git (push)
internal	git@code.example.com:infra/myproj.git (fetch)
internal	git@code.example.com:infra/myproj.git (push)
`
	got := parseGitRemoteV(in)
	if len(got) != 4 {
		t.Fatalf("expected 4 raw entries, got %d", len(got))
	}
	if got[0].Name != "origin" || got[0].URL != "https://github.com/alice/myproj.git" {
		t.Errorf("got[0]=%+v", got[0])
	}
	if got[2].Name != "internal" || got[2].URL != "git@code.example.com:infra/myproj.git" {
		t.Errorf("got[2]=%+v", got[2])
	}
}

func TestParseGitRemoteV_HandlesEmptyAndBlank(t *testing.T) {
	got := parseGitRemoteV("")
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
	got = parseGitRemoteV("\n\n  \n")
	if len(got) != 0 {
		t.Errorf("expected 0, got %d", len(got))
	}
}

func TestDetect_ReadsGitRemotes(t *testing.T) {
	if _, err := Detect(t.Context(), ""); err == nil {
		t.Fatal("Detect accepted an empty repo root")
	}
	dir := t.TempDir()
	if _, err := Detect(t.Context(), dir); err == nil {
		t.Fatal("Detect accepted a directory outside git")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:alice/myproj.git"},
		{"remote", "add", "internal", "https://code.example.com/infra/myproj.git"},
	} {
		if _, err := gitexec.Output(t.Context(), dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	got, err := Detect(t.Context(), dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	want := []Remote{
		{Name: "internal", Host: "code.example.com", Type: RemoteTypeForgejo, Repo: "infra/myproj"},
		{Name: "origin", Host: "github.com", Type: RemoteTypeGitHub, Repo: "alice/myproj"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got: %+v\nwant: %+v", got, want)
	}
}

func TestValidRepo(t *testing.T) {
	for repo, want := range map[string]bool{
		"alice/my-proj.go": true,
		"alice":            false,
		"a/b/c":            false,
		"../b":             false,
		"a/.":              false,
		"a/b c":            false,
		"/b":               false,
	} {
		if got := ValidRepo(repo); got != want {
			t.Errorf("ValidRepo(%q) = %v, want %v", repo, got, want)
		}
	}
}
