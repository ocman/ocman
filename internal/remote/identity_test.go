package remote

import "testing"

func TestNormalizeProjectIdentity(t *testing.T) {
	cases := []struct {
		origin, dir, want string
	}{
		{"git@github.com:Org/Repo.git", "/x/repo", "github.com/org/repo"},
		{"https://github.com/Org/Repo", "/x/repo", "github.com/org/repo"},
		{"https://github.com/Org/Repo.git", "/x/repo", "github.com/org/repo"},
		{"ssh://git@host.example:22/org/repo.git", "/x/repo", "host.example/org/repo"},
		{"http://gitlab.local/group/sub/proj.git", "/x", "gitlab.local/group/sub/proj"},
		{"", "/home/u/myapp", "basename:myapp"},
		{"", "/home/u/myapp/", "basename:myapp"},
	}
	for _, c := range cases {
		if got := NormalizeProjectIdentity(c.origin, c.dir); got != c.want {
			t.Errorf("NormalizeProjectIdentity(%q, %q) = %q, want %q", c.origin, c.dir, got, c.want)
		}
	}
}
