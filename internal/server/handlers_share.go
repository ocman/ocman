package server

import (
	"context"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

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
func (s *Server) sharingEnabled() (bool, error) {
	if s.stateDB == nil {
		return true, nil
	}
	v, ok, err := s.stateDB.GetSetting(settingSharingEnabled)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	return v != "false", nil
}

// shareTokenPattern reuses the same safe-character constraint as
// validateID plus the base64url alphabet characters. base64.RawURLEncoding
// emits [A-Za-z0-9_-], all of which validateID already permits, so we
// can validate share tokens with the existing helper.

// --- Authenticated: per-session export + share management ---

// handleSessionExportMarkdown serves GET /api/session/{id}/export.md.
// Returns the full conversation rendered as Markdown. Auth-gated like
// every other /api/session/ route.
func (s *Server) handleSessionExportMarkdown(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Strip the trailing "/export.md" — sessionSubPath returns it as
	// part of the id only when there is no slash, which there is, so id
	// is already clean. Defensive trim in case of future routing tweaks.
	sessionID = strings.TrimSuffix(sessionID, "/export.md")

	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	md, err := s.renderConversationMarkdown(r.Context(), adapter, sessionID)
	if err != nil {
		writePlatformError(w, "exporting session", err)
		return
	}
	writeMarkdownDownload(w, sessionID, md)
}

// handleSessionShares serves GET /api/session/{id}/shares: lists the
// active share links for the session.
func (s *Server) handleSessionShares(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if s.stateDB == nil {
			writeJSON(w, []shareLinkView{})
			return
		}
		links, err := s.stateDB.ListActiveShareLinks(string(adapter.ID()), sessionID)
		if err != nil {
			serverError(w, "listing share links", err)
			return
		}
		writeJSON(w, s.shareLinkViews(r, links))
	})
}

// handleCreateSessionShare serves POST /api/session/{id}/share: mints a
// new share link for the session and returns it (token + absolute URL).
func (s *Server) handleCreateSessionShare(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if s.stateDB == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		enabled, err := s.sharingEnabled()
		if err != nil {
			log.WithError(err).Error("reading sharing setting")
			http.Error(w, "sharing state unavailable", http.StatusServiceUnavailable)
			return
		}
		if !enabled {
			http.Error(w, "sharing is disabled", http.StatusForbidden)
			return
		}
		if s.relayURL == "" {
			http.Error(w, "sharing needs a share relay: start ocman with -relay-url, or set OCMAN_RELAY_URL",
				http.StatusServiceUnavailable)
			return
		}
		// expiresAt 0 = no expiry (the only mode the current UI uses).
		link, err := s.stateDB.CreateShareLink(string(adapter.ID()), sessionID, 0)
		if err != nil {
			serverError(w, "creating share link", err)
			return
		}
		link, err = s.createRelayShare(r.Context(), link, adapter)
		if err != nil {
			// The local row is useless without its relay copy, so drop
			// it rather than leaving a link that resolves to nothing.
			if _, rerr := s.stateDB.RevokeShareLink(string(adapter.ID()), sessionID, link.Token); rerr != nil {
				log.WithError(rerr).Warn("rolling back share link after relay failure")
			}
			writeShareRelayError(w, s.relayURL, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, s.shareLinkView(r, link))
	})
}

// handleRevokeSessionShare serves DELETE /api/session/{id}/share/{token}.
func (s *Server) handleRevokeSessionShare(w http.ResponseWriter, r *http.Request) {
	// Path is /api/session/{id}/share/{token}; withSessionAdapter gives
	// us id + rest ("share/{token}").
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string, adapter platforms.Platform) {
		token := strings.TrimPrefix(rest, "share/")
		if token == "" || !validateID(token) {
			http.Error(w, "invalid share token", http.StatusBadRequest)
			return
		}
		if s.stateDB == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		link, _, _ := s.stateDB.GetActiveShareLink(token)
		revoked, err := s.stateDB.RevokeShareLink(string(adapter.ID()), sessionID, token)
		if err != nil {
			serverError(w, "revoking share link", err)
			return
		}
		if !revoked {
			http.Error(w, "share link not found", http.StatusNotFound)
			return
		}
		if link.RelayID != "" {
			allocation := share.RelayAllocation{ID: link.RelayID, DeleteToken: link.RelayDeleteToken}
			_ = s.relayClient(link.RelayURL).Delete(r.Context(), allocation)
		}
		w.WriteHeader(http.StatusNoContent)
	})
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
func (s *Server) sharingSettingView(enabled bool) sharingSettingView {
	return sharingSettingView{
		Enabled:     enabled,
		RelayURL:    s.relayURL,
		RelaySource: s.relaySource,
	}
}

// handleSharingSetting dispatches GET/POST on /api/settings/sharing.
//
//	GET  → the current settings. Sharing defaults to enabled when unset.
//	POST → accepts {"enabled": bool}, persists it, returns the new state.
//
// The relay fields are returned by both methods so a client that toggles
// sharing keeps a complete picture without a second request.
func (s *Server) handleSharingSetting(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		enabled, err := s.sharingEnabled()
		if err != nil {
			log.WithError(err).Error("reading sharing setting")
			http.Error(w, "sharing state unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, s.sharingSettingView(enabled))
	case http.MethodPost:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		val := "true"
		if !body.Enabled {
			val = "false"
		}
		if err := s.stateDB.SetSetting(settingSharingEnabled, val); err != nil {
			serverError(w, "saving sharing setting", err)
			return
		}
		writeJSON(w, s.sharingSettingView(body.Enabled))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAllShares serves GET /api/shares: every active share link across
// all sessions, so the Settings page can inspect and revoke them in one
// place.
func (s *Server) handleAllShares(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		writeJSON(w, []globalShareLinkView{})
		return
	}
	links, err := s.stateDB.ListAllActiveShareLinks()
	if err != nil {
		serverError(w, "listing share links", err)
		return
	}
	out := make([]globalShareLinkView, 0, len(links))
	for _, l := range links {
		out = append(out, globalShareLinkView{
			shareLinkView: s.shareLinkView(r, l),
			Platform:      l.Platform,
			SessionID:     l.SessionID,
		})
	}
	writeJSON(w, out)
}

// --- shared helpers ---

// renderConversationMarkdown fetches the full conversation for a session
// and renders it to Markdown via conversationMarkdown.
func (s *Server) renderConversationMarkdown(ctx context.Context, adapter platforms.Platform, sessionID string) (string, error) {
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

func (s *Server) shareLinkView(r *http.Request, link state.ShareLink) shareLinkView {
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

func (s *Server) shareLinkViews(r *http.Request, links []state.ShareLink) []shareLinkView {
	out := make([]shareLinkView, 0, len(links))
	for _, l := range links {
		out = append(out, s.shareLinkView(r, l))
	}
	return out
}
