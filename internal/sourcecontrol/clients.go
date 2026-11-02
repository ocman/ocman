package sourcecontrol

import (
	"os"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
)

// Clients are the hub's forge clients discovered from this machine's
// environment, gh and tea configuration. Saved credential overrides are
// layered on top by the module.
type Clients struct {
	GitHub  *github.Client
	Forgejo *forgejo.Registry
}

// DiscoverClients reads this machine's forge credentials.
func DiscoverClients() *Clients {
	return &Clients{
		GitHub:  github.New(),
		Forgejo: forgejo.NewRegistry(),
	}
}

// Connections describes each discovered client and the credential source
// it uses, without secrets. A nil Clients has none.
func (c *Clients) Connections() []Discovered {
	connections := []Discovered{}
	if c == nil {
		return connections
	}
	if gh := c.GitHub; gh != nil {
		source := "anonymous"
		if gh.Token() != "" {
			source = "GitHub CLI"
		}
		if os.Getenv("GITHUB_TOKEN") != "" {
			source = "GITHUB_TOKEN"
		} else if os.Getenv("GH_TOKEN") != "" {
			source = "GH_TOKEN"
		}
		connections = append(connections, Discovered{Connection: Connection{Host: "github.com", Provider: forge.RemoteTypeGitHub, Source: source}, Client: gh})
	}
	if reg := c.Forgejo; reg != nil {
		for _, host := range reg.Hosts() {
			source := "tea login"
			if os.Getenv("FORGEJO_TOKEN") != "" {
				source = "FORGEJO_TOKEN"
			} else if os.Getenv("GITEA_TOKEN") != "" {
				source = "GITEA_TOKEN"
			}
			connections = append(connections, Discovered{Connection: Connection{Host: host, Provider: forge.RemoteTypeForgejo, Source: source}, Client: reg.ForHost(host)})
		}
	}
	return connections
}
