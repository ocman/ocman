package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// exportFetchLimit is the per-fetch message cap used when assembling a
// full conversation for export/share. Platform.Session paginates from
// the newest message, so a large limit effectively returns the whole
// conversation while still bounding pathological cases.
const exportFetchLimit = 100000

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
		// expiresAt 0 = no expiry (the only mode the current UI uses).
		link, err := s.stateDB.CreateShareLink(string(adapter.ID()), sessionID, 0)
		if err != nil {
			serverError(w, "creating share link", err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, s.shareLinkView(r, link))
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
		revoked, err := s.stateDB.RevokeShareLink(string(adapter.ID()), sessionID, token)
		if err != nil {
			serverError(w, "revoking share link", err)
			return
		}
		if !revoked {
			http.Error(w, "share link not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// --- Public (unauthenticated) share viewing ---

// handleSharePublic dispatches the public /api/share/{token} routes.
// Unauthenticated by design: the token is the only credential.
//
//	GET /api/share/{token}            -> conversation JSON (read-only)
//	GET /api/share/{token}/export.md  -> conversation Markdown
func (s *Server) handleSharePublic(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/share/")
	token := rest
	wantMarkdown := false
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		token = rest[:idx]
		sub := rest[idx+1:]
		if sub == "export.md" {
			wantMarkdown = true
		} else {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	if token == "" || !validateID(token) {
		http.Error(w, "share link not found", http.StatusNotFound)
		return
	}
	if s.stateDB == nil {
		http.Error(w, "share link not found", http.StatusNotFound)
		return
	}

	link, ok, err := s.stateDB.GetActiveShareLink(token)
	if err != nil {
		serverError(w, "resolving share link", err)
		return
	}
	if !ok {
		// Unknown, revoked, or expired all collapse to 404 so a
		// revoked token can't be distinguished from a never-existing one.
		http.Error(w, "share link not found", http.StatusNotFound)
		return
	}

	adapter, ok := s.registry.Get(platforms.ID(link.Platform))
	if !ok {
		http.Error(w, "share link not found", http.StatusNotFound)
		return
	}

	if wantMarkdown {
		md, err := s.renderConversationMarkdown(r.Context(), adapter, link.SessionID)
		if err != nil {
			writePlatformError(w, "exporting shared session", err)
			return
		}
		writeMarkdownDownload(w, link.SessionID, md)
		return
	}

	detail, err := adapter.Session(r.Context(), link.SessionID, exportFetchLimit, 0)
	if err != nil {
		writePlatformError(w, "loading shared session", err)
		return
	}
	if detail.Messages == nil {
		detail.Messages = []db.Message{}
	}
	if detail.Parts == nil {
		detail.Parts = []db.Part{}
	}
	if detail.Session != nil {
		detail.Session.Notice = deriveSessionNotice(*detail.Session)
	}
	// The public payload deliberately omits live-only / actionable
	// fields the read-only viewer doesn't need; the frontend renders
	// purely from session/messages/parts.
	writeJSON(w, map[string]interface{}{
		"session":  detail.Session,
		"messages": detail.Messages,
		"parts":    detail.Parts,
		"readOnly": true,
	})
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
	Token     string `json:"token"`
	URL       string `json:"url"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

func (s *Server) shareLinkView(r *http.Request, link state.ShareLink) shareLinkView {
	return shareLinkView{
		Token:     link.Token,
		URL:       s.shareURL(r, link.Token),
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

// shareURL builds the absolute, user-facing URL for a share token. It
// uses the configured publicBaseURL when set, otherwise derives the
// origin from the incoming request (scheme + Host header) so localhost /
// dev works without any configuration.
func (s *Server) shareURL(r *http.Request, token string) string {
	base := s.publicBaseURL
	if base == "" {
		base = requestOrigin(r)
	}
	return strings.TrimRight(base, "/") + "/share/" + token
}

// requestOrigin reconstructs the "scheme://host" the client used. It
// honours X-Forwarded-Proto (set by reverse proxies) and falls back to
// TLS state, defaulting to http for plain localhost.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost:8228"
	}
	return scheme + "://" + host
}
