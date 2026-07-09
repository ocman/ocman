package local

import (
	"encoding/json"
	"testing"
)

// TestBuildExternalDirectoryPermission checks the OPENCODE_PERMISSION JSON
// carries an external_directory allow rule scoped to the given worktrees
// root — and only that root (no blanket allow).
func TestBuildExternalDirectoryPermission(t *testing.T) {
	root := "/home/u/src/.worktrees/ocman"
	js, err := buildExternalDirectoryPermission(root)
	if err != nil {
		t.Fatalf("buildExternalDirectoryPermission: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, js)
	}

	ext, ok := got["external_directory"].(map[string]any)
	if !ok {
		t.Fatalf("external_directory missing or wrong type: %v", got)
	}
	want := root + "/**"
	rule, ok := ext[want]
	if !ok {
		t.Fatalf("expected a rule keyed by %q, got keys %v", want, ext)
	}
	if rule != "allow" {
		t.Errorf("rule = %v; want \"allow\"", rule)
	}
	// Must NOT blanket-allow all external directories.
	if _, blanket := ext["*"]; blanket {
		t.Error("must not contain a blanket \"*\" external_directory rule")
	}
	if _, blanket := ext["**"]; blanket {
		t.Error("must not contain a blanket \"**\" external_directory rule")
	}
	if len(ext) != 1 {
		t.Errorf("expected exactly one external_directory rule, got %d: %v", len(ext), ext)
	}
}

// TestMergeOpencodePermission verifies the helper merges the
// external_directory rule into an inherited permission set rather than
// overwriting it (composes with #101).
func TestMergeOpencodePermission(t *testing.T) {
	inherited := `{"bash":{"*":"ask"},"external_directory":{"/other/**":"allow"}}`
	merged, err := mergeExternalDirectoryPermission(inherited, "/wt/ocman")
	if err != nil {
		t.Fatalf("mergeExternalDirectoryPermission: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	// Inherited bash rule preserved.
	if _, ok := got["bash"]; !ok {
		t.Error("inherited bash rule was dropped")
	}
	ext := got["external_directory"].(map[string]any)
	// Inherited external_directory rule preserved.
	if ext["/other/**"] != "allow" {
		t.Error("inherited external_directory rule was dropped")
	}
	// New rule added.
	if ext["/wt/ocman/**"] != "allow" {
		t.Errorf("new external_directory rule missing: %v", ext)
	}
}

// TestMergeOpencodePermission_EmptyInherited handles the no-inherited case.
func TestMergeOpencodePermission_EmptyInherited(t *testing.T) {
	merged, err := mergeExternalDirectoryPermission("", "/wt/ocman")
	if err != nil {
		t.Fatalf("mergeExternalDirectoryPermission empty: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	ext := got["external_directory"].(map[string]any)
	if ext["/wt/ocman/**"] != "allow" {
		t.Errorf("rule missing: %v", ext)
	}
}
