package autoapprove

import (
	"context"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
)

// PromptSessionID is the session that issued a prompt: a subagent's prompt
// carries its own session, else it belongs to fallback.
func PromptSessionID(entry platforms.LivePrompt, fallback string) string {
	if sessionID, _ := entry["sessionID"].(string); sessionID != "" {
		return sessionID
	}
	return fallback
}

// PromptPatterns reads a prompt's "patterns", tolerating []string and the
// []any a generic json.Unmarshal produces.
func PromptPatterns(entry platforms.LivePrompt) []string { return entry.StringsField("patterns") }

// PromptMetadata reads a prompt's "metadata" object, nil when absent.
func PromptMetadata(entry platforms.LivePrompt) map[string]any {
	m, _ := entry["metadata"].(map[string]any)
	return m
}

// isRemote reports a compound remote platform id: the owner runs its own
// pipeline, so the hub never judges or approves a remote's prompts.
func isRemote(id platforms.ID) bool { return strings.HasPrefix(string(id), "r-") }

// EnsurePending records and judges prompts listed outside the event stream
// (page reload, a prompt that predates the stream). Ensure dedups against
// the stream, so a prompt seen both ways is judged once.
func (m *Module) EnsurePending(adapter platforms.Platform, sessionID string, entries []platforms.LivePrompt) {
	m.eachPermission(entries, sessionID, func(sid, id, permission string, entry platforms.LivePrompt) {
		m.svc.ObservePermissionPrompt(adapter.ID(), sessionID, entry)
		m.svc.Ensure(adapter.ID(), adapter, sid, id, permission, PromptPatterns(entry), PromptMetadata(entry))
	})
}

// resumePending judges the session's pending prompts once auto-approve is
// enabled. Best effort: the toggle already succeeded.
func (m *Module) resumePending(ctx context.Context, adapter platforms.Platform, sessionID string) {
	if isRemote(adapter.ID()) {
		return
	}
	entries, err := adapter.ListPermissions(ctx, sessionID)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"sessionID": sessionID, "platform": adapter.ID()}).Warn("auto-approve toggle: failed to list pending permissions for resume")
		return
	}
	m.eachPermission(entries, sessionID, func(sid, id, permission string, entry platforms.LivePrompt) {
		m.svc.Ensure(adapter.ID(), adapter, sid, id, permission, PromptPatterns(entry), PromptMetadata(entry))
	})
}

// ApplyRulesToPending approves every prompt pending on the session, or on a
// subagent of it, that its new permission rules allow. A remote owner runs
// this itself when the hub forwards the rules change.
func (m *Module) ApplyRulesToPending(ctx context.Context, adapter platforms.Platform, sessionID string) {
	if isRemote(adapter.ID()) {
		return
	}
	entries, err := adapter.ListPermissions(ctx, sessionID)
	if err != nil {
		log.WithError(err).WithField("sessionID", sessionID).Warn("permission rules: failed to list pending permissions")
		return
	}
	m.eachPermission(entries, sessionID, func(sid, id, permission string, entry platforms.LivePrompt) {
		m.svc.ApplySessionRules(ctx, adapter.ID(), adapter, sid, id, permission, PromptPatterns(entry), PromptMetadata(entry))
	})
}

func (m *Module) eachPermission(entries []platforms.LivePrompt, sessionID string, f func(promptSessionID, permissionID, permission string, entry platforms.LivePrompt)) {
	for _, entry := range entries {
		permissionID, _ := entry["id"].(string)
		permission, _ := entry["permission"].(string)
		if permissionID == "" || permission == "" {
			continue
		}
		f(PromptSessionID(entry, sessionID), permissionID, permission, entry)
	}
}
