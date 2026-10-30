package prworkflow

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/ocman/ocman/internal/forge"
)

// Explicit repository identity is independent of the session's checkout.
// ResolveForge selects only configured hub clients, never a caller-supplied API.
func explicitRemote(req changeRequest) (forge.Remote, error) {
	u, err := url.Parse("https://" + req.Host)
	if err != nil || req.Host == "" || u.Host != req.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(req.Host, " /\\\t\r\n") || !forge.ValidRepo(req.Repo) || req.Remote != "" {
		return forge.Remote{}, fmt.Errorf("Link requires either remote or a valid host and owner/repository pair, not both")
	}
	rem := forge.Remote{Host: req.Host, Repo: req.Repo, Type: forge.RemoteTypeForgejo}
	if rem.Host == "github.com" {
		rem.Type = forge.RemoteTypeGitHub
	}
	return rem, nil
}

func (m *Module) externalLink(ctx context.Context, t Target, req changeRequest) (Link, error) {
	rem, err := explicitRemote(req)
	if err != nil {
		return Link{}, err
	}
	if err := m.checkOwner(ctx, Link{Platform: t.Platform, SessionID: t.SessionID, Directory: t.Directory, RemoteID: t.RemoteID}); err != nil {
		return Link{}, err
	}
	f, ok := m.d.ResolveForge(rem)
	if !ok {
		return Link{}, fmt.Errorf("forge unavailable")
	}
	pr, err := f.LookupPR(ctx, rem.Repo, req.Number)
	if err != nil {
		return Link{}, err
	}
	return Link{Platform: t.Platform, SessionID: t.SessionID, Directory: t.Directory, RemoteID: t.RemoteID, Remote: rem, Number: req.Number, PR: pr}, nil
}
