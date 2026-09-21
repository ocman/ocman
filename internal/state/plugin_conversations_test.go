package state

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func conversationKey(account, thread string) PluginConversationKey {
	return PluginConversationKey{PluginID: "org.example.chat", AccountID: account, ThreadID: thread}
}

// TestPluginConversationSurvivesRestart is the durability requirement: a thread
// mapped before a restart still resolves to its session, in both directions.
func TestPluginConversationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	requirePluginOK(t, err)
	key := conversationKey("T1", "C1:1.0")
	session := PluginConversationSession{PlatformID: "opencode", SessionID: "ses-1"}
	linked, won, err := d.LinkPluginConversation(t.Context(), key, session)
	requirePluginOK(t, err)
	if !won || linked != session {
		t.Fatalf("first claim lost: %+v %v", linked, won)
	}
	requirePluginOK(t, d.Close())

	d, err = Open(path)
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = d.Close() })
	got, ok, err := d.GetPluginConversation(t.Context(), key)
	requirePluginOK(t, err)
	if !ok || got != session {
		t.Fatalf("mapping lost across restart: %+v %v", got, ok)
	}
	reverse, ok, err := d.GetPluginConversationThread(t.Context(), session)
	requirePluginOK(t, err)
	if !ok || reverse != key {
		t.Fatalf("reverse lookup lost across restart: %+v %v", reverse, ok)
	}
}

// TestPluginConversationClaimIsAtomic covers concurrent first messages: the
// insert-if-absent must admit exactly one session as the mapping.
func TestPluginConversationClaimIsAtomic(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = d.Close() })
	key := conversationKey("T1", "C1:1.0")

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, results := 0, map[PluginConversationSession]int{}
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := PluginConversationSession{PlatformID: "opencode", SessionID: "ses-" + string(rune('a'+i))}
			linked, won, err := d.LinkPluginConversation(t.Context(), key, candidate)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("racer %d: %v", i, err)
				return
			}
			if won {
				wins++
			}
			results[linked]++
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d racers won the claim, want exactly 1", wins)
	}
	if len(results) != 1 || results[mustMapping(t, d, key)] != racers {
		t.Fatalf("racers disagree on the mapped session: %+v", results)
	}
}

// TestPluginConversationIsolation covers the keys a naive mapping would merge:
// another workspace reusing a thread identity, another thread in the same
// workspace, and another plugin entirely.
func TestPluginConversationIsolation(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = d.Close() })
	keys := []PluginConversationKey{
		conversationKey("T1", "C1:1.0"),
		conversationKey("T2", "C1:1.0"),
		conversationKey("T1", "C2:1.0"),
		{PluginID: "org.example.other", AccountID: "T1", ThreadID: "C1:1.0"},
	}
	for i, key := range keys {
		session := PluginConversationSession{PlatformID: "opencode", SessionID: "ses-" + string(rune('a'+i))}
		if _, won, err := d.LinkPluginConversation(t.Context(), key, session); err != nil || !won {
			t.Fatalf("%+v was merged into an existing mapping: %v %v", key, won, err)
		}
	}
	// The same bare session id on another machine is a different session.
	local := PluginConversationSession{PlatformID: "opencode", SessionID: "ses-shared"}
	remoteSession := PluginConversationSession{PlatformID: "r-A:opencode", SessionID: "ses-shared"}
	requireClaim(t, d, conversationKey("T1", "C3:1.0"), local)
	requireClaim(t, d, conversationKey("T1", "C4:1.0"), remoteSession)
	owner, ok, err := d.GetPluginConversationThread(t.Context(), remoteSession)
	requirePluginOK(t, err)
	if !ok || owner.ThreadID != "C4:1.0" {
		t.Fatalf("remote session resolved to the local thread: %+v", owner)
	}
}

func TestPluginConversationRejectsIncompleteIdentity(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = d.Close() })
	session := PluginConversationSession{PlatformID: "opencode", SessionID: "ses-1"}
	for name, key := range map[string]PluginConversationKey{
		"no plugin":  {AccountID: "T1", ThreadID: "C1:1.0"},
		"no account": {PluginID: "org.example.chat", ThreadID: "C1:1.0"},
		"no thread":  {PluginID: "org.example.chat", AccountID: "T1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := d.LinkPluginConversation(t.Context(), key, session); !errors.Is(err, ErrPluginInvalid) {
				t.Fatalf("accepted %+v: %v", key, err)
			}
			if _, _, err := d.GetPluginConversation(t.Context(), key); !errors.Is(err, ErrPluginInvalid) {
				t.Fatalf("read accepted %+v: %v", key, err)
			}
		})
	}
	key := conversationKey("T1", "C1:1.0")
	if _, _, err := d.LinkPluginConversation(t.Context(), key, PluginConversationSession{SessionID: "ses-1"}); !errors.Is(err, ErrPluginInvalid) {
		t.Fatalf("accepted an unowned session: %v", err)
	}
	if _, _, err := d.GetPluginConversationThread(t.Context(), PluginConversationSession{}); !errors.Is(err, ErrPluginInvalid) {
		t.Fatal("accepted an empty session identity")
	}
	if _, ok, err := d.GetPluginConversationThread(t.Context(), session); err != nil || ok {
		t.Fatalf("unmapped session resolved: %v %v", ok, err)
	}
}

func requireClaim(t *testing.T, d *DB, key PluginConversationKey, session PluginConversationSession) {
	t.Helper()
	if _, won, err := d.LinkPluginConversation(t.Context(), key, session); err != nil || !won {
		t.Fatalf("claim for %+v failed: %v %v", key, won, err)
	}
}

func mustMapping(t *testing.T, d *DB, key PluginConversationKey) PluginConversationSession {
	t.Helper()
	session, ok, err := d.GetPluginConversation(t.Context(), key)
	requirePluginOK(t, err)
	if !ok {
		t.Fatalf("no mapping for %+v", key)
	}
	return session
}
