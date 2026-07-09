package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestAaSvcSessionDirResolver exercises the SessionDir closure wired in
// aaSvc(): with no OpenCode DB it must error (so the judge falls
// through to human review) rather than resolving a directory.
func TestAaSvcSessionDirResolver_NoDB(t *testing.T) {
	srv := &Server{} // db == nil
	if _, err := srv.aaSvc().ResolveSessionDir("ses-1"); err == nil {
		t.Fatal("expected an error resolving session dir with no DB, got nil")
	}
}

// TestAaSvcOpencodeAdapter covers the OpencodePlatform closure: nil
// registry and an empty registry both yield nil; a registered OpenCode
// adapter is returned.
func TestAaSvcOpencodeAdapter(t *testing.T) {
	// Empty registry: no opencode adapter registered.
	empty := &Server{registry: platforms.NewRegistry()}
	if a := empty.aaSvc().OpencodeAdapter(); a != nil {
		t.Fatalf("expected nil adapter from empty registry, got %v", a)
	}

	// Registry with a fake "opencode" adapter: it must be returned.
	reg := platforms.NewRegistry()
	fp := &fakePlatform{id: "opencode"}
	reg.Register(fp)
	srv := &Server{registry: reg}
	if a := srv.aaSvc().OpencodeAdapter(); a == nil {
		t.Fatal("expected the registered opencode adapter, got nil")
	}
}

// TestAaSvcParentSessionIDResolver covers the ParentSessionID closure
// wired in aaSvc(): a tracked child resolves to its parent; an unknown
// session and a server with no state DB both miss (so the child simply
// doesn't inherit rather than erroring).
func TestAaSvcParentSessionIDResolver(t *testing.T) {
	// No state DB → always miss.
	if _, ok := (&Server{}).aaSvc().ResolveParentSessionID("child"); ok {
		t.Fatal("expected miss with no state DB")
	}

	sdb := openWatcherTestStateDB(t)
	insertWatcherChildSession(t, sdb, "child-1", "parent-1", "running")
	srv := &Server{stateDB: sdb}

	if parent, ok := srv.aaSvc().ResolveParentSessionID("child-1"); !ok || parent != "parent-1" {
		t.Fatalf("ResolveParentSessionID(child-1) = (%q,%v); want (parent-1,true)", parent, ok)
	}
	if _, ok := srv.aaSvc().ResolveParentSessionID("unknown"); ok {
		t.Fatal("expected miss for an untracked session")
	}
}
