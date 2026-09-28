package previewauth

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// refreshSkew refreshes tokens slightly before their reported expiry.
const refreshSkew = time.Minute

// AccessToken returns a usable access token for a viewer's workspace,
// refreshing it first when it is about to expire. Concurrent callers share
// one refresh so a rotating refresh token is redeemed exactly once.
func (m *Manager) AccessToken(ctx context.Context, viewerID, ownerID, providerID, workspaceID string) (string, error) {
	p, ok := m.providers[providerID]
	if !ok {
		return "", ErrUnknownProvider
	}
	key := strings.Join([]string{viewerID, ownerID, providerID, workspaceID}, "\x00")
	v, err, _ := m.refresh.Do(key, func() (any, error) {
		c, err := m.db.PreviewCredential(ctx, viewerID, ownerID, providerID, workspaceID)
		if errors.Is(err, state.ErrPreviewNotFound) {
			return "", ErrNotConnected
		}
		if err != nil {
			return "", err
		}
		if c.ExpiresAt.IsZero() || time.Now().Add(refreshSkew).Before(c.ExpiresAt) {
			return c.AccessToken, nil
		}
		if c.RefreshToken == "" {
			return "", ErrExpired
		}
		tok, err := m.tokenRequest(ctx, p, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}})
		if errors.Is(err, ErrRevoked) {
			_ = m.Revoked(ctx, viewerID, ownerID, providerID, workspaceID)
			return "", ErrRevoked
		}
		if err != nil {
			return "", err
		}
		c.AccessToken, c.ExpiresAt = tok.AccessToken, tok.ExpiresAt
		if tok.RefreshToken != "" {
			c.RefreshToken = tok.RefreshToken
		}
		if err := m.db.PutPreviewCredential(ctx, c); err != nil {
			return "", err
		}
		return c.AccessToken, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Revoked forgets a grant the provider rejected (401 or invalid_grant).
func (m *Manager) Revoked(ctx context.Context, viewerID, ownerID, providerID, workspaceID string) error {
	_, err := m.db.DeletePreviewCredentials(ctx, viewerID, ownerID, providerID, workspaceID)
	return err
}

// Disconnect deletes a viewer's grants (all workspaces when workspaceID is
// empty) and revokes them at the provider on a best-effort basis.
func (m *Manager) Disconnect(ctx context.Context, viewerID, ownerID, providerID, workspaceID string) error {
	if _, ok := m.providers[providerID]; !ok {
		return ErrUnknownProvider
	}
	gone, err := m.db.DeletePreviewCredentials(ctx, viewerID, ownerID, providerID, workspaceID)
	m.revokeAll(ctx, gone)
	return err
}

func (m *Manager) revokeAll(ctx context.Context, creds []state.PreviewCredential) {
	seen := map[string]bool{}
	for _, c := range creds {
		p, ok := m.providers[c.Provider]
		// ponytail: a pasted token is revoked at the provider only when an app
		// exists too (then the OAuth revoke endpoint just rejects it).
		if !ok || p.RevokeURL == "" || !p.OAuth() || seen[c.AccessToken] {
			continue
		}
		seen[c.AccessToken] = true
		form := url.Values{"token": {c.AccessToken}}
		if !p.BasicAuth {
			form.Set("client_id", p.ClientID)
			if p.ClientSecret != "" {
				form.Set("client_secret", p.ClientSecret)
			}
		}
		req, err := p.post(ctx, p.RevokeURL, form)
		if err != nil {
			continue
		}
		if resp, err := m.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
}

// Connection is the client-safe view of one grant: display names only.
type Connection struct {
	WorkspaceID   string              `json:"workspaceId"`
	WorkspaceName string              `json:"workspaceName"`
	AccountName   string              `json:"accountName"`
	Sites         []state.PreviewSite `json:"sites"`
	State         string              `json:"state"` // connected | expired
}

// ProviderStatus is the client-safe status of one provider for a viewer.
type ProviderStatus struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Notice      string       `json:"notice,omitempty"`
	OAuth       bool         `json:"oauth"`
	TokenHelp   string       `json:"tokenHelp,omitempty"`
	Connections []Connection `json:"connections"`
}

// Status lists every configured provider with the viewer's connections.
func (m *Manager) Status(ctx context.Context, viewerID, ownerID string) ([]ProviderStatus, error) {
	creds, err := m.db.PreviewCredentials(ctx, viewerID, ownerID, "")
	if err != nil {
		return nil, err
	}
	byProvider := map[string][]Connection{}
	for _, c := range creds {
		st := "connected"
		if !c.Refreshable && !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt) {
			st = "expired"
		}
		byProvider[c.Provider] = append(byProvider[c.Provider], Connection{
			WorkspaceID: c.WorkspaceID, WorkspaceName: c.WorkspaceName, AccountName: c.AccountName,
			Sites: append([]state.PreviewSite{}, c.Sites...), State: st,
		})
	}
	out := make([]ProviderStatus, 0, len(m.providers))
	for id, p := range m.providers {
		conns := byProvider[id]
		if conns == nil {
			conns = []Connection{}
		}
		out = append(out, ProviderStatus{ID: id, Name: p.Name, Notice: p.Notice, OAuth: p.OAuth(), TokenHelp: p.TokenHelp, Connections: conns})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
