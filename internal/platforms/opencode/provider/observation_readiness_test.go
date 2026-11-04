package opencode

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func TestMaintenanceWaitsForStatusAndPromptBootstrap(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	a.rt.writeCachedPorts(map[string]string{"/repo": "7777"})
	if _, err := a.SessionObservationsReady(t.Context()); err == nil {
		t.Fatal("unseeded status was trusted")
	}
	a.SeedSessionStatus("7777", 0, 0, map[string]string{"native": "busy"})
	if _, err := a.SessionObservationsReady(t.Context()); err == nil {
		t.Fatal("unseeded prompts were trusted")
	}
	for _, kind := range []string{"permission", "question"} {
		scope := promptScope("/repo", kind)
		a.prompts.scopePort[scope] = "7777"
		a.prompts.applySnapshot("/repo", kind, 1, nil, nil)
	}
	if connected, err := a.SessionObservationsReady(t.Context()); err != nil || !connected {
		t.Fatalf("seeded source: %v %v", connected, err)
	}
	a.prompts.scopePort[promptScope("/repo/worktree", "question")] = "7777"
	if _, err := a.SessionObservationsReady(t.Context()); err == nil {
		t.Fatal("unseeded worktree was trusted")
	}
	a.ClearPromptsForPort("7777")
	if _, err := a.SessionObservationsReady(t.Context()); err == nil {
		t.Fatal("disconnected prompts were trusted")
	}
	a.rt.writeCachedPorts(map[string]string{})
	if connected, err := a.SessionObservationsReady(t.Context()); err != nil || connected {
		t.Fatalf("offline source: %v %v", connected, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.SessionObservationsReady(ctx); err == nil {
		t.Fatal("failed discovery was trusted")
	}
}
