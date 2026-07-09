package local

import "encoding/json"

// OPENCODE_PERMISSION seeding for the one-opencode-per-project launcher.
//
// OpenCode loads OPENCODE_PERMISSION (inline JSON) at launch, taking
// precedence over project/global config. `external_directory` is a
// first-class permission key whose value is a map of glob -> decision
// ("allow" / "ask" / "deny"). We seed a single rule scoped to the
// project's .worktrees/<repo> root so in-app worktree sessions can touch
// their own worktrees without blanket-allowing every external directory.
//
// The build/merge helpers are kept small and dependency-free so #101
// (which also writes OPENCODE_PERMISSION at launch) can merge its own
// rules into the same document.

// buildExternalDirectoryPermission returns an OPENCODE_PERMISSION JSON
// document that allows external_directory access scoped to
// worktreesRoot/** and nothing else.
func buildExternalDirectoryPermission(worktreesRoot string) (string, error) {
	return mergeExternalDirectoryPermission("", worktreesRoot)
}

// mergeExternalDirectoryPermission merges a scoped external_directory
// allow rule for worktreesRoot/** into an inherited OPENCODE_PERMISSION
// document (JSON, may be empty), preserving any existing keys and rules.
// It never overwrites the inherited set — it only adds the one rule.
func mergeExternalDirectoryPermission(inherited, worktreesRoot string) (string, error) {
	doc := map[string]any{}
	if inherited != "" {
		if err := json.Unmarshal([]byte(inherited), &doc); err != nil {
			return "", err
		}
	}

	ext, _ := doc["external_directory"].(map[string]any)
	if ext == nil {
		ext = map[string]any{}
	}
	ext[worktreesRoot+"/**"] = "allow"
	doc["external_directory"] = ext

	b, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
