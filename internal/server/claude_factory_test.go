package server

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/factory/model"
	"github.com/ocman/ocman/internal/factorymod"
	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// claudeFactoryLauncher is the real implementation launcher with the
// workspace preparation (fetch/target resolution against a forge) stubbed.
type claudeFactoryLauncher struct {
	factorymod.ImplementationLauncher
}

func (claudeFactoryLauncher) PrepareImplementationWorkspace(context.Context, string, string, string, string) (string, string, error) {
	// This fixture deliberately uses a local snapshot, not the fake forge URL.
	return "main", "refs/heads/main", nil
}

// An Epic created with platform claudecode plans and dispatches its
// implementation Attempt to Claude sessions, whatever the default platform.
func TestClaudeFactoryEpicDispatchesToClaude(t *testing.T) {
	srv, adapter, _ := newClaudeServer(t)
	repo := claudeRepo(t)
	if out, err := gitexec.Command(t.Context(), "-C", repo, "remote", "add", "origin", "https://github.com/acme/repo.git").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := srv.stateDB.SetSetting(t.Context(), state.DefaultPlatformSetting, "opencode"); err != nil {
		t.Fatal(err)
	}
	svc := factory.NewNativeWithExecution(srv.stateDB, factoryProjectResolver{server: srv}, srv.factoryModule().PlanningLauncher(),
		claudeFactoryLauncher{srv.factoryModule().ImplementationLauncher()})
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	epic, err := svc.CreateWorkEpic(t.Context(), factory.CreateWorkEpicRequest{Goal: "Claude Epic", InitialProject: repo, Platform: "claudecode",
		AcknowledgeLocalExecution: true, FormulaID: "ocman/tracer", FormulaRevision: 2})
	if err != nil || epic.Platform != "claudecode" {
		t.Fatalf("epic = %+v, %v", epic, err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	issueID := func(kind string) string {
		issues, err := svc.ListIssues(t.Context(), epic.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, issue := range issues {
			if issue.Kind == kind {
				return issue.ID
			}
		}
		t.Fatalf("missing %s issue", kind)
		return ""
	}
	plan, err := svc.ClaimPlan(t.Context(), epic.ID, issueID("plan"))
	if err != nil || plan.Session.Platform != "claudecode" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), factory.SubmitProposalRequest{EpicID: epic.ID, Manifest: factory.ProposalManifest{EpicID: epic.ID, MolID: issueID("mol"), Project: repo,
		Nodes: []factory.ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", factory.PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash, ImplementationModel: "anthropic/claude-sonnet-4-5"}); err != nil {
		t.Fatal(err)
	}
	var impl *model.FactoryAttempt
	deadline := time.Now().Add(5 * time.Second)
	for impl == nil {
		saved, err := svc.GetWorkEpic(t.Context(), epic.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i, attempt := range saved.Attempts {
			if attempt.ID != plan.Attempt.ID && attempt.Phase == model.FactoryAttemptActive {
				impl = &saved.Attempts[i]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("implementation did not start: %+v", saved.Attempts)
		}
		if impl == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if impl.Session.Platform != "claudecode" {
		t.Fatalf("implementation = %+v", impl)
	}
	if dir := claudeSessionDir(t, adapter, impl.Session.ID); !strings.Contains(dir, ".worktrees") {
		t.Fatalf("implementation session directory = %q", dir)
	}
	// Claude asks for every MCP call; Factory's own tool is pre-approved.
	allowFactory := platforms.PermissionRule{Permission: "mcp__ocman__factory", Pattern: "*", Action: "allow"}
	for _, id := range []string{plan.Session.ID, impl.Session.ID} {
		rules, err := adapter.PermissionRules(t.Context(), id)
		if err != nil || !slices.Contains(rules, allowFactory) {
			t.Fatalf("rules for %s = %+v, %v", id, rules, err)
		}
	}
}
