package forge

import (
	"reflect"
	"sort"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

func TestClassifyRemotes_HTTPSGitHub(t *testing.T) {
	in := []rawRemote{
		{Name: "origin", URL: "https://github.com/alice/myproj.git"},
	}
	got := classifyRemotes(in)
	want := []Remote{{
		Name: "origin",
		Host: "github.com",
		Type: RemoteTypeGitHub,
		Repo: "alice/myproj",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got: %+v\nwant: %+v", got, want)
	}
}

func TestClassifyRemotes_DoesNotExposeCredentials(t *testing.T) {
	got := classifyRemotes([]rawRemote{{
		Name: "origin", URL: "https://user:secret@github.com/alice/myproj.git",
	}})
	if len(got) != 1 || got[0].URL != "" {
		t.Fatalf("remote = %+v, want no raw URL", got)
	}
}

func TestClassifyRemotes_RejectsUnsafeRepositoryPaths(t *testing.T) {
	for _, remoteURL := range []string{
		"https://github.com/../user/issues.git",
		"https://github.com/owner/repo/extra.git",
		"https://github.com/owner/%2e%2e.git",
		"x:y@github.com",
	} {
		if got := classifyRemotes([]rawRemote{{Name: "origin", URL: remoteURL}}); len(got) != 0 {
			t.Fatalf("classifyRemotes(%q) = %+v, want no remote", remoteURL, got)
		}
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

func TestClassifyRemotes_SSHGitHub(t *testing.T) {
	in := []rawRemote{
		{Name: "origin", URL: "git@github.com:alice/myproj.git"},
	}
	got := classifyRemotes(in)
	if len(got) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(got))
	}
	if got[0].Host != "github.com" || got[0].Type != RemoteTypeGitHub || got[0].Repo != "alice/myproj" {
		t.Errorf("got %+v", got[0])
	}
}

func TestClassifyRemotes_HTTPSForgejo(t *testing.T) {
	in := []rawRemote{
		{Name: "internal", URL: "https://code.example.com/infra/myproj.git"},
	}
	got := classifyRemotes(in)
	if len(got) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(got))
	}
	if got[0].Host != "code.example.com" || got[0].Type != RemoteTypeForgejo || got[0].Repo != "infra/myproj" {
		t.Errorf("got %+v", got[0])
	}
}

func TestClassifyRemotes_SSHForgejo(t *testing.T) {
	in := []rawRemote{
		{Name: "internal", URL: "git@code.example.com:infra/myproj.git"},
	}
	got := classifyRemotes(in)
	if len(got) != 1 {
		t.Fatalf("expected 1 remote, got %d", len(got))
	}
	if got[0].Host != "code.example.com" || got[0].Type != RemoteTypeForgejo {
		t.Errorf("got %+v", got[0])
	}
}

// Detection runs on the owner, which does not know the hub's Forgejo
// hosts: every non-GitHub host is typed Forgejo and the hub narrows it.
func TestClassifyRemotes_NonGitHubHostsAreForgejo(t *testing.T) {
	in := []rawRemote{
		{Name: "gitlab", URL: "https://gitlab.com/x/y.git"},
		{Name: "origin", URL: "https://github.com/a/b.git"},
	}
	got := classifyRemotes(in)
	want := []Remote{
		{Name: "gitlab", Host: "gitlab.com", Type: RemoteTypeForgejo, Repo: "x/y"},
		{Name: "origin", Host: "github.com", Type: RemoteTypeGitHub, Repo: "a/b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\n got: %+v\nwant: %+v", got, want)
	}
}

func TestClassifyRemotes_DeduplicatesByRemoteName(t *testing.T) {
	// `git remote -v` lists each remote twice (fetch + push). The
	// parser should yield one entry per remote name, not two.
	in := []rawRemote{
		{Name: "origin", URL: "https://github.com/a/b.git"},
		{Name: "origin", URL: "https://github.com/a/b.git"},
	}
	got := classifyRemotes(in)
	if len(got) != 1 {
		t.Fatalf("expected 1 remote after dedup, got %d", len(got))
	}
}

func TestClassifyRemotes_NoTrailingDotGit(t *testing.T) {
	// URLs without .git suffix should still parse correctly.
	in := []rawRemote{
		{Name: "origin", URL: "https://github.com/a/b"},
	}
	got := classifyRemotes(in)
	if got[0].Repo != "a/b" {
		t.Errorf("expected 'a/b', got %q", got[0].Repo)
	}
}

func TestClassifyRemotes_StableOrder(t *testing.T) {
	// Order returned should be stable so the frontend can render
	// groups deterministically. Sort by remote name.
	in := []rawRemote{
		{Name: "upstream", URL: "https://github.com/u/r.git"},
		{Name: "origin", URL: "https://github.com/o/r.git"},
		{Name: "fork", URL: "https://github.com/f/r.git"},
	}
	got := classifyRemotes(in)
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	if !sort.StringsAreSorted(names) {
		t.Errorf("expected sorted names, got %v", names)
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
