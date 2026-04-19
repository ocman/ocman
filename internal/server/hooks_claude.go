package server

import (
	"encoding/json"
	"io"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	claudecodeplatform "github.com/NoUseFreak/ocman/internal/platforms/claudecode"
)

// handleClaudeHook receives events posted by Claude Code's hook
// system and feeds them into the live-state cache of the claude-code
// adapter.
//
// Wire shape (set by our installer — see installer.go):
//
//	POST /api/hooks/claude
//	Content-Type: application/json  (body fed from Claude CLI stdin)
//
// Responses:
//
//   - 200 {"ok":true}                       — event processed
//   - 200 {"ok":true,"ignored":true}        — valid JSON but unknown event
//     name / empty session_id; hook should exit 0 anyway
//   - 400 {"error":"..."}                   — malformed JSON
//   - 204 (no body)                         — no Claude Code adapter
//     registered (ocman built / started without claude-code support);
//     hook still exits 0 so the CLI doesn't surface an error to the user
//
// The handler is registered under requireLocalhost — hook events are
// only trustworthy when they come from a process on the same machine
// as ocman. See D2 in the Phase 5 plan.
func (s *Server) handleClaudeHook(w http.ResponseWriter, r *http.Request) {
	// A missing adapter is a configuration state, not an error. Log
	// once and reply 204 so the CLI's curl exits 0.
	adapter, ok := s.registry.Get(claudecodeplatform.PlatformID)
	if !ok {
		log.Debug("claude hook received but adapter is not registered")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Hook payloads are small (a few kB at most), but defend against
	// runaway bodies anyway. maxRequestBody is the same 1 MB cap
	// applied to other JSON endpoints.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		http.Error(w, "reading hook payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Downcast the Platform interface to the concrete adapter to
	// reach ApplyHookEvent — the method is intentionally NOT part of
	// the Platform interface (it's a Claude-specific event sink,
	// nothing else implements it).
	cc, ok := adapter.(*claudecodeplatform.Adapter)
	if !ok {
		// Defensive: registry returned something labelled
		// "claude-code" that isn't our adapter. Should never happen;
		// treat as "no adapter".
		log.WithField("actual_type", typeName(adapter)).
			Warn("claude hook: registered adapter is not *claudecode.Adapter")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := cc.ApplyHookEvent(r.Context(), body); err != nil {
		// Only malformed JSON gets here — unknown event names and
		// missing session_id are absorbed inside ApplyHookEvent and
		// return nil.
		log.WithError(err).Debug("claude hook payload rejected")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Decode just enough to tell the caller whether we acted. Keeps
	// the response body stable regardless of which branch parse took.
	ignored := false
	var peek struct {
		SessionID     string `json:"session_id"`
		HookEventName string `json:"hook_event_name"`
	}
	if json.Unmarshal(body, &peek) == nil {
		// Mirror the ignore rules in parseHookPayload. Keep these in
		// sync if the parser's semantics change.
		if peek.SessionID == "" {
			ignored = true
		} else if _, known := recognisedHookEvents[peek.HookEventName]; !known {
			ignored = true
		}
	}

	resp := map[string]interface{}{"ok": true}
	if ignored {
		resp["ignored"] = true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// recognisedHookEvents mirrors the closed set in the adapter's
// hooks_parse.go. Duplicated here rather than exported from the
// package because (a) it's small and stable, and (b) the handler
// needs only the key membership, not the value.
var recognisedHookEvents = map[string]struct{}{
	"UserPromptSubmit": {},
	"SessionStart":     {},
	"PreToolUse":       {},
	"PostToolUse":      {},
	"Stop":             {},
	"SubagentStop":     {},
	"Notification":     {},
}

// typeName is a tiny helper so the warn-log above doesn't need a
// dependency on `reflect`.
func typeName(p platforms.Platform) string {
	if p == nil {
		return "<nil>"
	}
	return string(p.ID()) + "(unknown-go-type)"
}
