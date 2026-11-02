package sourcecontrol

import (
	"testing"

	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
)

func TestForgeConnectionSources(t *testing.T) {
	if len((*Clients)(nil).Connections()) != 0 {
		t.Fatal("nil integrations")
	}
	in := &Clients{GitHub: github.NewWithCredentials("http://unused", "token", nil), Forgejo: forgejo.NewRegistryForTest(map[string]*forgejo.Client{"code.example.com": forgejo.NewWithCredentials("code.example.com", "http://unused", "token", nil)})}
	for _, vars := range [][2]string{{"GITHUB_TOKEN", "FORGEJO_TOKEN"}, {"GH_TOKEN", "GITEA_TOKEN"}, {"", ""}} {
		for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN", "FORGEJO_TOKEN", "GITEA_TOKEN"} {
			t.Setenv(key, "")
		}
		for _, key := range vars {
			if key != "" {
				t.Setenv(key, "test")
			}
		}
		connections := in.Connections()
		if len(connections) != 2 {
			t.Fatal(connections)
		}
		if vars[0] != "" && connections[0].Source != vars[0] {
			t.Fatal(connections)
		}
		if vars[1] != "" && connections[1].Source != vars[1] {
			t.Fatal(connections)
		}
	}
}
