package server

import (
	"context"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// --- Auto-approve endpoints ---

// handleSessionAutoApproveGet handles GET /api/session/{id}/auto-approve.
// Returns the effective auto-approve state for the session: the per-session
// override when set, otherwise the server-wide default.
func (s *Server) handleSessionAutoApproveGet(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		enabled, exists, err := s.stateDB.GetAutoApprove(string(adapter.ID()), sessionID)
		if err != nil {
			serverError(w, "getting auto-approve state", err)
			return
		}
		effective := s.autoApproveDefault
		if exists {
			effective = enabled
		}
		writeJSON(w, map[string]interface{}{
			"enabled":    effective,
			"overridden": exists,
		})
	})
}

// handleSessionAutoApproveSet handles POST /api/session/{id}/auto-approve.
// Body: {"enabled": true|false}
//
// When toggling from disabled to enabled, the handler additionally
// kicks off the auto-approve judge for any *already-pending* permission
// in this session. Without this, a user who sees a permission prompt
// and only then clicks "Enable auto-approve" would be stuck on the
// "starting…" UI forever: the original permission.asked event already
// fired (and was discarded because auto-approve was off at the time),
// no new event ever arrives for the same prompt, and the frontend
// doesn't refetch permissions on toggle. See
// PermissionPrompt.tsx fallback at "Auto-approve on · starting…".
func (s *Server) handleSessionAutoApproveSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		if err := s.stateDB.SetAutoApprove(string(adapter.ID()), sessionID, req.Enabled); err != nil {
			serverError(w, "setting auto-approve state", err)
			return
		}
		// When enabling, resurrect any pending permissions so the judge
		// runs on prompts that arrived while auto-approve was off.
		// ensureAutoApprove dedups against in-flight goroutines and
		// recorded verdicts, so this is safe to call unconditionally.
		// Errors here are non-fatal — the toggle itself succeeded; at
		// worst the user still has to answer the existing prompt
		// manually.
		if req.Enabled {
			s.resumeAutoApproveForPending(r.Context(), adapter, sessionID)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// resumeAutoApproveForPending lists every pending permission for the
// session and kicks off ensureAutoApprove on each. Called from the
// auto-approve enable path so prompts that arrived while auto-approve
// was off get judged as soon as the user opts in.
//
// Errors listing permissions are logged but otherwise swallowed: the
// caller is the toggle handler, which has already persisted the new
// state and must not fail on a best-effort resurrection.
func (s *Server) resumeAutoApproveForPending(ctx context.Context, adapter platforms.Platform, sessionID string) {
	entries, err := adapter.ListPermissions(ctx, sessionID)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"sessionID": sessionID,
			"platform":  adapter.ID(),
		}).Warn("auto-approve toggle: failed to list pending permissions for resume")
		return
	}
	for _, entry := range entries {
		permissionID, _ := entry["id"].(string)
		permission, _ := entry["permission"].(string)
		if permissionID == "" || permission == "" {
			continue
		}
		patterns := extractPermissionPatterns(entry)
		metadata := extractPermissionMetadata(entry)
		s.aaSvc().Ensure(adapter.ID(), adapter, sessionID, permissionID, permission, patterns, metadata)
	}
}

// handleSessionApprovedPermissions handles GET /api/session/{id}/approved-permissions.
// Returns all permissions that were auto-approved by the LLM judge for this
// session, ordered by approval time. Used by the frontend to re-inject
// approval notices into the conversation thread after a page refresh.
func (s *Server) handleSessionApprovedPermissions(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		approved, err := s.stateDB.ListApprovedPermissions(string(adapter.ID()), sessionID)
		if err != nil {
			serverError(w, "listing approved permissions", err)
			return
		}
		type entry struct {
			PermissionID string   `json:"permissionId"`
			Permission   string   `json:"permission"`
			Patterns     []string `json:"patterns"`
			Reasoning    string   `json:"reasoning"`
			ApprovedAt   int64    `json:"approvedAt"`
		}
		out := make([]entry, 0, len(approved))
		for _, p := range approved {
			patterns := p.Patterns
			if patterns == nil {
				patterns = []string{}
			}
			out = append(out, entry{
				PermissionID: p.PermissionID,
				Permission:   p.PermissionText,
				Patterns:     patterns,
				Reasoning:    p.Reasoning,
				ApprovedAt:   p.ApprovedAt,
			})
		}
		writeJSON(w, out)
	})
}
