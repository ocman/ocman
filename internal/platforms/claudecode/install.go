package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ocmanHookOwner is the sentinel string we stamp on every hook entry
// we manage. Reinstalls keep only entries without our sentinel and
// replace the tagged ones.
//
// Chosen to be stable across versions so a future ocman can still
// recognise entries it installed five releases ago.
const ocmanHookOwner = "ocman"

// managedHookEvents lists the Claude Code hook events ocman installs.
// Keep this in sync with the event-name switch in hooks_parse.go —
// anything outside this list will be parsed as Ignored anyway, so
// there's no value in installing it.
//
// Order is deterministic so the generated JSON is stable across runs
// (important for the idempotence test: same input -> same bytes ->
// same mtime).
var managedHookEvents = []string{
	"UserPromptSubmit",
	"SessionStart",
	"PreToolUse",
	"PostToolUse",
	"Stop",
	"SubagentStop",
	"Notification",
}

// InstallHooks writes (or updates) ocman-owned hook entries in the
// Claude Code settings.json located at settingsPath. hookURL is the
// full URL the hook's curl command will POST to — typically
// http://127.0.0.1:<port>/api/hooks/claude.
//
// Behaviour:
//
//   - If settingsPath does not exist, a fresh file is created with a
//     hooks block (and the parent directory is ensured to exist).
//   - Existing top-level keys are preserved.
//   - For each managed event, user-authored entries stay; the single
//     ocman-owned entry (tagged with _owner="ocman") is replaced.
//   - If the produced file bytes are identical to the on-disk bytes,
//     no write happens — mtime stays stable, VCS watches don't flap.
//
// Errors:
//
//   - Existing settings.json is not valid JSON (installer refuses to
//     clobber a broken hand-edited file).
//   - Write fails (permissions, disk full, ...).
//
// Note that "hooks exist but the installer failed" must be treated as
// non-fatal by callers — the server should still boot and fall back
// to no live-state tracking.
func InstallHooks(settingsPath, hookURL string) error {
	// Load whatever currently exists. Missing file is fine.
	existing := map[string]interface{}{}
	if raw, err := os.ReadFile(settingsPath); err == nil {
		// Empty file is treated as empty object. Anything else must
		// parse or we bail out.
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &existing); err != nil {
				return fmt.Errorf("existing settings.json is not valid JSON: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", settingsPath, err)
	}

	// Pull out the hooks sub-object (or create it).
	hooks, _ := existing["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}

	for _, event := range managedHookEvents {
		hooks[event] = mergeEventEntries(hooks[event], buildOcmanHookEntry(hookURL))
	}
	existing["hooks"] = hooks

	// Serialise with a stable key order + trailing newline so the
	// file reads naturally in an editor.
	out, err := marshalStable(existing)
	if err != nil {
		return fmt.Errorf("marshal settings.json: %w", err)
	}

	// Idempotence: don't rewrite if the bytes match.
	if current, err := os.ReadFile(settingsPath); err == nil && bytes.Equal(current, out) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(settingsPath), err)
	}
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", settingsPath, err)
	}
	return nil
}

// mergeEventEntries takes whatever is currently stored for a given
// event name (typically a []interface{} of entry objects) and returns
// a new slice with every non-ocman entry preserved plus one fresh
// ocman entry.
//
// Acts as both "insert" (when current is nil) and "replace" (when
// current already has our sentinel entry).
func mergeEventEntries(current interface{}, ours map[string]interface{}) []interface{} {
	out := []interface{}{}
	if arr, ok := current.([]interface{}); ok {
		for _, e := range arr {
			m, ok := e.(map[string]interface{})
			if !ok {
				// Not a shape we recognise — preserve verbatim so
				// we don't eat hand-edited settings.
				out = append(out, e)
				continue
			}
			if m["_owner"] == ocmanHookOwner {
				// Skip: we'll append our fresh copy below.
				continue
			}
			out = append(out, m)
		}
	}
	out = append(out, ours)
	return out
}

// buildOcmanHookEntry returns the canonical hook entry ocman installs
// for each managed event. The entry itself carries the _owner
// sentinel so we can recognise it on a future reinstall, even if the
// command string inside drifts.
//
// The command is a curl pipeline that:
//   - Runs with --max-time 2 so a dead ocman can't stall the CLI.
//   - Pipes stdin (the hook payload) into the request body via
//     --data-binary @-. --data (non-binary) would mangle whitespace.
//   - Silences curl's progress + errors and always exits 0, because
//     a non-zero exit from a hook command propagates to the CLI.
func buildOcmanHookEntry(hookURL string) map[string]interface{} {
	cmd := fmt.Sprintf(
		"curl -sS -X POST --max-time 2 -H 'Content-Type: application/json' --data-binary @- %s >/dev/null 2>&1 || true",
		hookURL,
	)
	return map[string]interface{}{
		"_owner":  ocmanHookOwner,
		"matcher": "*",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": cmd,
				"timeout": 3, // 2s curl + 1s slack
			},
		},
	}
}

// marshalStable JSON-encodes v with 2-space indent and a trailing
// newline. Uses the standard encoding/json which sorts map keys
// alphabetically, giving us a byte-stable output per input.
func marshalStable(v interface{}) ([]byte, error) {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(buf, '\n'), nil
}
