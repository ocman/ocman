package state

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestPrematureAgentGraphEditsPreserveInitialPlanning(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, action := range []string{"create", "edit"} {
			t.Run(action+map[bool]string{false: "-unclaimed", true: "-active"}[active], func(t *testing.T) {
				db := openTestStateDB(t)
				defer db.Close()
				ctx := t.Context()
				epic, err := db.CreateFactoryEpic(ctx, "", "Initial plan", "", "/repo", "", nativeTracerFormula(t))
				if err != nil {
					t.Fatal(err)
				}
				mol := factoryIssueID(t, db, epic.ID, "mol")
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Human draft"}); err != nil {
					t.Fatal(err)
				}
				plan := factoryIssueID(t, db, epic.ID, "plan")
				var attempt model.FactoryAttempt
				if active {
					_, attempt, err = db.ClaimFactoryPlan(ctx, epic.ID, plan, "factory-plan/v1", time.Now())
					if err != nil {
						t.Fatal(err)
					}
					if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "planner"}, time.Now()); err != nil || !changed {
						t.Fatalf("activate = %v, %v", changed, err)
					}
				}
				mutation := model.GraphMutation{Actor: "mcp", Action: action, EpicID: epic.ID, ParentID: mol, IssueID: issueIDWithTitle(t, db, epic.ID, "Human draft"), Kind: "task", Title: "Agent change"}
				if err := db.MutateFactoryGraph(ctx, mutation); !errors.Is(err, model.ErrInvalidGraphMutation) {
					t.Fatalf("premature edit was accepted: %v", err)
				}
				if _, err := db.GetFactoryPlanGate(ctx, epic.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("premature edit created a gate: %v", err)
				}
				if got := issueByID(t, db, epic.ID, mutation.IssueID); got.Title != "Human draft" {
					t.Fatalf("draft changed: %#v", got)
				}
				if active {
					if got, found, err := db.GetFactoryAttempt(ctx, attempt.ID); err != nil || !found || got.Phase != model.FactoryAttemptActive {
						t.Fatalf("planner changed: %#v, %v", got, err)
					}
				}
				proposal := model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: "/repo", ManifestJSON: `{"nodes":[]}`, ContentHash: "initial"}
				if active {
					if _, authorized, err := db.SaveFactoryProposalRevisionForAttempt(ctx, proposal, attempt.ID, attempt.AgentToken); err != nil || !authorized {
						t.Fatalf("initial planner cannot submit: %v, %v", authorized, err)
					}
				} else if _, _, err := db.ImportFactoryProposalRevision(ctx, proposal); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestProposedRemovalCannotReleaseAnotherEpicsConsumer(t *testing.T) {
	for _, dependency := range []string{"blocks", "on_failure", "merge_gated", "nested"} {
		t.Run(dependency, func(t *testing.T) {
			db := openTestStateDB(t)
			defer db.Close()
			ctx := t.Context()
			create := func(title, project string) (string, string, string) {
				epic, err := db.CreateFactoryEpic(ctx, "", title, "", project, "", nativeTracerFormula(t))
				if err != nil {
					t.Fatal(err)
				}
				mol := factoryIssueID(t, db, epic.ID, "mol")
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: title + " work"}); err != nil {
					t.Fatal(err)
				}
				proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: project, ManifestJSON: `{"nodes":[]}`, ContentHash: title})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
					t.Fatal(err)
				}
				return epic.ID, mol, issueIDWithTitle(t, db, epic.ID, title+" work")
			}
			source, mol, blocker := create("Source", "/source")
			consumerEpic, _, consumer := create("Consumer", "/consumer")
			removed := blocker
			if dependency == "nested" {
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: source, ParentID: mol, Kind: "mol", Title: "Nested group"}); err != nil {
					t.Fatal(err)
				}
				removed = issueIDWithTitle(t, db, source, "Nested group")
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "reparent", EpicID: source, IssueID: blocker, ParentID: removed}); err != nil {
					t.Fatal(err)
				}
				dependency = "blocks"
			}
			if dependency == "merge_gated" {
				if err := db.EnsureFactoryDeliveryIssue(ctx, source); err != nil {
					t.Fatal(err)
				}
				blocker = factoryIssueID(t, db, source, "delivery")
				removed = blocker
			}
			if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/consumer", "factory-implement", "v1", "user", time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: consumerEpic, IssueID: consumer, DependsOnID: blocker, DependencyType: dependency}); err != nil {
				t.Fatal(err)
			}
			if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Actor: "mcp", Action: "delete", EpicID: source, IssueID: removed}); err != nil {
				t.Fatal(err)
			}
			if got := issueByID(t, db, consumerEpic, consumer); got.DispatchState != "waiting" || len(got.Blockers) != 1 {
				t.Fatalf("unapproved removal released consumer: %#v", got)
			}
			if _, _, err := db.ClaimFactoryImplementation(ctx, consumerEpic, consumer, "factory-implement/v1", time.Now()); err == nil {
				t.Fatal("claimed consumer before source approval")
			}
			if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Actor: "mcp", Action: "create", EpicID: source, ParentID: mol, Kind: "task", Title: "Further pending work"}); err != nil {
				t.Fatal(err)
			}
			if got := issueByID(t, db, consumerEpic, consumer); got.DispatchState != "waiting" {
				t.Fatalf("later pending revision released consumer: %#v", got)
			}
			gate, err := db.GetFactoryPlanGate(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.DecideFactoryPlanGate(ctx, source, "approve", gate.ProposalRevision, gate.ProposalHash, ""); err != nil {
				t.Fatal(err)
			}
			if got := issueByID(t, db, consumerEpic, consumer); got.DispatchState != "ready" {
				t.Fatalf("approved removal did not release consumer: %#v", got)
			}
			// A later unrelated amendment must not revive an approved removal.
			if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Actor: "mcp", Action: "create", EpicID: source, ParentID: mol, Kind: "task", Title: "Other work"}); err != nil {
				t.Fatal(err)
			}
			if got := issueByID(t, db, consumerEpic, consumer); got.DispatchState != "ready" {
				t.Fatalf("old removal was revived: %#v", got)
			}
			if _, _, err := db.ClaimFactoryImplementation(ctx, consumerEpic, consumer, "factory-implement/v1", time.Now()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
