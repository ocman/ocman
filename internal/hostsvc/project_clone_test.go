package hostsvc

import "testing"

func TestSafeCloneURL(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://example.com/Org/Repo.git", "https://example.com/Org/Repo.git"},
		{"https://user:secret@example.com/Org/Repo.git", "https://example.com/Org/Repo.git"},
		{"git@example.com:Org/Repo.git", "ssh://git@example.com/Org/Repo.git"},
		{"ssh://git@example.com:2222/Org/Repo.git", "ssh://git@example.com:2222/Org/Repo.git"},
		{"ext::sh -c evil", ""}, {"file:///secret", ""}, {"/local/repo", ""},
		{"https://example.com/a?token=secret", ""}, {"https://example.com/a#secret", ""},
		{"ssh://git:secret@example.com/repo", ""}, {"ssh://-oProxyCommand@example.com/repo", ""},
		{"https://example.com/../repo", ""}, {"https://example.com/%2e%2e/repo", ""},
		{"https://example.com/%00repo", ""}, {"https://example.com/", ""},
		{"git@example.com:/absolute", ""}, {"https://example.com/a\nb", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := SafeCloneURL(tc.input)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
