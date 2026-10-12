package sharing

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
)

// exportFetchLimit is the per-fetch message cap used when assembling a
// full conversation for export/share. Platform.Session paginates from
// the newest message, so a large limit effectively returns the whole
// conversation while still bounding pathological cases.
const exportFetchLimit = 100000

// settingSharingEnabled gates whether new share links can be minted.
// Stored in state.db's `setting` table; absent means enabled (on by
// default). Value "false" disables creation.
const settingSharingEnabled = "sharing_enabled"

// sharingEnabled reports whether share-link creation is allowed. Absent
// setting (or any value other than "false") means enabled. A read error
// is returned, never swallowed: callers must fail closed, otherwise a
// transient DB error silently re-enables minting public unauthenticated
// links after an operator turned sharing off.
func (m *Module) sharingEnabled(ctx context.Context) (bool, error) {
	v, ok, err := m.Store.GetSetting(ctx, settingSharingEnabled)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	return v != "false", nil
}

// tokenPattern is base64.RawURLEncoding's alphabet, the share token's.
var tokenPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validToken(token string) bool { return len(token) <= 256 && tokenPattern.MatchString(token) }

// --- Authenticated: per-session export + share management ---

// handleSessionExportMarkdown serves GET /api/session/{id}/export.md.
// Returns the full conversation rendered as Markdown. Auth-gated like
// every other /api/session/ route.
func (m *Module) handleExportMarkdown(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	md, err := m.renderConversationMarkdown(r.Context(), adapter, sessionID)
	if err != nil {
		httpx.WritePlatformError(w, "exporting session", err)
		return
	}
	writeMarkdownDownload(w, sessionID, md)
}

// handleSessionShares serves GET /api/session/{id}/shares: lists the
// active share links for the session.
func (m *Module) handleSessionShares(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	links, err := m.Store.ListActiveShareLinks(r.Context(), string(adapter.ID()), sessionID)
	if err != nil {
		httpx.ServerError(w, "listing share links", err)
		return
	}
	httpx.WriteJSON(w, m.shareLinkViews(r, links))
}

// handleCreateSessionShare serves POST /api/session/{id}/share: mints a
// new share link for the session and returns it (token + absolute URL).
func (m *Module) handleCreateSessionShare(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	enabled, err := m.sharingEnabled(r.Context())
	if err != nil {
		log.WithError(err).Error("reading sharing setting")
		http.Error(w, "sharing state unavailable", http.StatusServiceUnavailable)
		return
	}
	if !enabled {
		http.Error(w, "sharing is disabled", http.StatusForbidden)
		return
	}
	if m.RelayURL() == "" {
		http.Error(w, "sharing needs a share relay: start ocman with -relay-url, or set OCMAN_RELAY_URL",
			http.StatusServiceUnavailable)
		return
	}
	// expiresAt 0 = no expiry (the only mode the current UI uses).
	link, err := m.Store.CreateShareLink(r.Context(), string(adapter.ID()), sessionID, 0)
	if err != nil {
		httpx.ServerError(w, "creating share link", err)
		return
	}
	link, err = m.createRelayShare(r.Context(), link, adapter)
	if err != nil {
		// The local row is useless without its relay copy, so drop
		// it rather than leaving a link that resolves to nothing.
		if _, rerr := m.Store.RevokeShareLink(r.Context(), string(adapter.ID()), sessionID, link.Token); rerr != nil {
			log.WithError(rerr).Warn("rolling back share link after relay failure")
		}
		share.WriteRelayError(w, m.RelayURL(), err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, m.shareLinkView(r, link))
}

// handleRevokeSessionShare serves DELETE /api/session/{id}/share/{token}.
func (m *Module) handleRevokeSessionShare(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
	token := r.PathValue("token")
	if !validToken(token) {
		http.Error(w, "invalid share token", http.StatusBadRequest)
		return
	}
	link, _, _ := m.Store.GetActiveShareLink(r.Context(), token)
	revoked, err := m.Store.RevokeShareLink(r.Context(), string(adapter.ID()), sessionID, token)
	if err != nil {
		httpx.ServerError(w, "revoking share link", err)
		return
	}
	if !revoked {
		http.Error(w, "share link not found", http.StatusNotFound)
		return
	}
	if link.RelayID != "" {
		allocation := share.RelayAllocation{ID: link.RelayID, DeleteToken: link.RelayDeleteToken}
		_ = share.NewRelayClient(link.RelayURL).Delete(r.Context(), allocation)
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Sharing settings + global share list ---

// sharingSettingView is the wire shape of /api/settings/sharing.
//
// Enabled is a persisted, user-editable setting. RelayURL and
// RelaySource are read-only: they come from the -relay-url flag, the
// OCMAN_RELAY_URL environment variable, or the value baked into the
// build, and are reported so the Settings page can show an operator
// which relay this instance uses and where that value came from.
type sharingSettingView struct {
	Enabled bool `json:"enabled"`
	// RelayURL is empty when no relay is configured, which means shares
	// stay local to this machine.
	RelayURL string `json:"relayUrl"`
	// RelaySource is "flag", "env", or "builtin", and empty when there
	// is no relay.
	RelaySource string `json:"relaySource"`
}

// sharingSettingView builds the current sharing settings payload.
func (m *Module) sharingSettingView(enabled bool) sharingSettingView {
	return sharingSettingView{
		Enabled:     enabled,
		RelayURL:    m.RelayURL(),
		RelaySource: m.RelaySource(),
	}
}

// handleSharingSetting dispatches GET/POST on /api/settings/sharing.
//
//	GET  → the current settings. Sharing defaults to enabled when unset.
//	POST → accepts {"enabled": bool}, persists it, returns the new state.
//
// The relay fields are returned by both methods so a client that toggles
// sharing keeps a complete picture without a second request.
func (m *Module) handleSharingSetting(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		enabled, err := m.sharingEnabled(r.Context())
		if err != nil {
			log.WithError(err).Error("reading sharing setting")
			http.Error(w, "sharing state unavailable", http.StatusServiceUnavailable)
			return
		}
		httpx.WriteJSON(w, m.sharingSettingView(enabled))
	case http.MethodPost:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		val := "true"
		if !body.Enabled {
			val = "false"
		}
		if err := m.Store.SetSetting(r.Context(), settingSharingEnabled, val); err != nil {
			httpx.ServerError(w, "saving sharing setting", err)
			return
		}
		httpx.WriteJSON(w, m.sharingSettingView(body.Enabled))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAllShares serves GET /api/shares: every active share link across
// all sessions, so the Settings page can inspect and revoke them in one
// place.
func (m *Module) handleAllShares(w http.ResponseWriter, r *http.Request) {
	links, err := m.Store.ListAllActiveShareLinks(r.Context())
	if err != nil {
		httpx.ServerError(w, "listing share links", err)
		return
	}
	out := make([]globalShareLinkView, 0, len(links))
	for _, l := range links {
		out = append(out, globalShareLinkView{
			shareLinkView: m.shareLinkView(r, l),
			Platform:      l.Platform,
			SessionID:     l.SessionID,
		})
	}
	httpx.WriteJSON(w, out)
}

// --- shared helpers ---

// renderConversationMarkdown fetches the full conversation for a session
// and renders it to Markdown via conversationMarkdown.
func (m *Module) renderConversationMarkdown(ctx context.Context, adapter platforms.Platform, sessionID string) (string, error) {
	detail, err := adapter.Session(ctx, sessionID, exportFetchLimit, 0)
	if err != nil {
		return "", err
	}
	return conversationMarkdown(detail.Session, detail.Messages, detail.Parts), nil
}

// writeMarkdownDownload writes a Markdown body with download headers.
func writeMarkdownDownload(w http.ResponseWriter, sessionID, md string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"conversation-"+sessionID+".md\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(md))
}

// shareLinkView is the wire shape returned for a share link, augmenting
// the stored row with the absolute, shareable URL.
type shareLinkView struct {
	Token string `json:"token"`
	// URL is always a relay URL; sharing never creates localhost links.
	URL       string `json:"url"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// globalShareLinkView augments a share link with its owning
// platform/session so the global Settings list can link back to the
// session and revoke via the existing per-session endpoint.
type globalShareLinkView struct {
	shareLinkView
	Platform  string `json:"platform"`
	SessionID string `json:"sessionId"`
}

func (m *Module) shareLinkView(r *http.Request, link state.ShareLink) shareLinkView {
	relayURL := ""
	if link.RelayID != "" {
		relayURL = strings.TrimRight(link.RelayURL, "/") + "/v/" + link.RelayID + "#k=" + link.RelayKey
	}
	return shareLinkView{
		Token:     link.Token,
		URL:       relayURL,
		CreatedAt: link.CreatedAt,
		ExpiresAt: link.ExpiresAt,
	}
}

func (m *Module) shareLinkViews(r *http.Request, links []state.ShareLink) []shareLinkView {
	out := make([]shareLinkView, 0, len(links))
	for _, l := range links {
		out = append(out, m.shareLinkView(r, l))
	}
	return out
}
