// Package permissions builds an inherited permission ruleset for a
// worktree/child session from a parent session's accumulated
// always-allow approvals (issue #101). It is a thin, dependency-light
// helper — no I/O of its own beyond the injected lister — so both the
// MCP split path and the /wt HTTP handler can reuse it.
package permissions

import (
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// maxInheritedPatterns caps the total number of "allow" rules emitted
// so a highly-active parent can't produce an unbounded ruleset. Above
// the cap the builder logs and truncates.
const maxInheritedPatterns = 500

// flatActionKeys are OpenCode permission keys that take no argument
// pattern — an allow rule for them uses an empty pattern.
var flatActionKeys = map[string]bool{
	"todowrite": true,
	"question":  true,
	"webfetch":  true,
	"websearch": true,
	"doom_loop": true,
}

// knownKeys is the allowlist of OpenCode permission keys we recognise.
// A stored PermissionText whose key is not in this set is dropped (with
// a debug log) rather than emitted, so schema drift can't inject a
// bogus permission into the child.
var knownKeys = map[string]bool{
	"read":               true,
	"edit":               true,
	"glob":               true,
	"grep":               true,
	"list":               true,
	"bash":               true,
	"task":               true,
	"external_directory": true,
	"todowrite":          true,
	"question":           true,
	"webfetch":           true,
	"websearch":          true,
	"repo_clone":         true,
	"repo_overview":      true,
	"lsp":                true,
	"doom_loop":          true,
	"skill":              true,
}

// ApprovalLister is the slice of *state.DB the builder needs.
type ApprovalLister interface {
	ListApprovedPermissions(platform, sessionID string) ([]state.ApprovedPermission, error)
}

// BuildInheritedRules reads the parent session's accumulated approvals
// and maps them to a PermissionRule set the child can be launched with.
//
// Rules:
//   - The permission key is parsed from ApprovedPermission.PermissionText
//     against knownKeys; unknown keys are dropped (debug log).
//   - Flat-action keys (todowrite, question, …) emit one allow rule with
//     an empty pattern.
//   - Patternable keys emit one allow rule per deduplicated pattern
//     (per key). A patternable key recorded with no patterns emits a
//     single "*" allow rule so the approval isn't silently lost.
//   - Total allow rules are capped at maxInheritedPatterns.
//
// An empty parent (no approvals) returns (nil, 0, nil).
func BuildInheritedRules(lister ApprovalLister, platform, parentSessionID string) ([]platforms.PermissionRule, int, error) {
	if lister == nil || parentSessionID == "" {
		return nil, 0, nil
	}
	approvals, err := lister.ListApprovedPermissions(platform, parentSessionID)
	if err != nil {
		return nil, 0, err
	}
	if len(approvals) == 0 {
		return nil, 0, nil
	}

	// Group deduplicated patterns per key, preserving first-seen order
	// of keys and patterns so the emitted ruleset is deterministic.
	var keyOrder []string
	patternsByKey := map[string][]string{}
	seen := map[string]bool{} // "key\x00pattern" -> true

	for _, ap := range approvals {
		key := permissionKey(ap.PermissionText)
		if !knownKeys[key] {
			log.WithField("permissionText", ap.PermissionText).
				Debug("permissions: dropping approval with unknown permission key")
			continue
		}
		if _, ok := patternsByKey[key]; !ok {
			keyOrder = append(keyOrder, key)
			patternsByKey[key] = nil
		}
		if flatActionKeys[key] {
			continue // flat keys carry no patterns
		}
		pats := ap.Patterns
		if len(pats) == 0 {
			pats = []string{"*"}
		}
		for _, p := range pats {
			dk := key + "\x00" + p
			if seen[dk] {
				continue
			}
			seen[dk] = true
			patternsByKey[key] = append(patternsByKey[key], p)
		}
	}

	var rules []platforms.PermissionRule
	for _, key := range keyOrder {
		if flatActionKeys[key] {
			rules = append(rules, platforms.PermissionRule{
				Permission: key,
				Pattern:    "",
				Action:     "allow",
			})
			continue
		}
		for _, p := range patternsByKey[key] {
			rules = append(rules, platforms.PermissionRule{
				Permission: key,
				Pattern:    p,
				Action:     "allow",
			})
		}
	}

	if len(rules) > maxInheritedPatterns {
		log.WithFields(log.Fields{
			"count": len(rules),
			"cap":   maxInheritedPatterns,
		}).Warn("permissions: inherited ruleset exceeds cap, truncating")
		rules = rules[:maxInheritedPatterns]
	}

	return rules, len(rules), nil
}

// permissionKey extracts the OpenCode permission key from a stored
// PermissionText. OpenCode records the key as the leading token; we
// take everything up to the first space so a richer label (e.g.
// "bash git status") still resolves to "bash". An exact match is the
// common case.
func permissionKey(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' {
			return text[:i]
		}
	}
	return text
}
