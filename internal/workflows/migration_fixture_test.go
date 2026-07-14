package workflows

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixtureExecutor supplies the two stable discovery items without executing
// Bun or Git. It keeps the preset test about orchestration contracts.
type fixtureExecutor struct{}

func (fixtureExecutor) Execute(_ context.Context, req CommandRequest) CommandResult {
	outputs := map[string]string{}
	for _, collector := range req.Outputs {
		value := "ok"
		if collector.Type == "json_file" || collector.Type == "json-file" {
			value = `[{"id":"one","path":"src/one.ts"},{"id":"two","path":"src/two.ts"}]`
		}
		outputs[collector.Name] = value
	}
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Outputs: outputs}
}

type fixtureAgent struct {
	mu       sync.Mutex
	next     int
	prompts  []string
}

func (a *fixtureAgent) Start(_ context.Context, req AgentRequest) (AgentSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.next++
	id := "fixture-agent-" + string(rune('0'+a.next))
	a.prompts = append(a.prompts, req.Prompt)
	return AgentSession{ID: id, Platform: req.Platform, State: "busy"}, nil
}

func (a *fixtureAgent) Inspect(_ context.Context, _ AgentSession, collectors []Collector) (AgentResult, error) {
	outputs := map[string]json.RawMessage{}
	for _, collector := range collectors {
		if collector.Type == "json_file" || collector.Type == "json-file" {
			outputs[collector.Name] = json.RawMessage(`{"approved":true,"findings":[]}`)
		} else {
			outputs[collector.Name] = json.RawMessage(`"fixture diff"`)
		}
	}
	return AgentResult{State: "done", Outputs: outputs}, nil
}

func (*fixtureAgent) Cancel(context.Context, AgentSession) error { return nil }

func readMigrationFixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "examples", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func fixtureForDirectory(t *testing.T, source []byte, directory string) []byte {
	t.Helper()
	return []byte(strings.ReplaceAll(string(source), "/workspace/repository", directory))
}

func TestAdversarialMigrationFixtureRunsTwoItems(t *testing.T) {
	h := newHarness(t)
	agent := &fixtureAgent{}
	h.svc = NewService(Deps{Store: h.db, Agent: agent, Blobs: h.blobs, CommandExecutor: fixtureExecutor{}, Now: func() time.Time { return h.now }})
	ctx := context.Background()
	directory := filepath.Dir(h.path)
	if _, err := h.svc.Publish(ctx, fixtureForDirectory(t, readMigrationFixture(t, "migration-item.yaml"), directory)); err != nil {
		t.Fatalf("publish item fixture: %v", err)
	}
	version, err := h.svc.Publish(ctx, fixtureForDirectory(t, readMigrationFixture(t, "adversarial-migration.yaml"), directory))
	if err != nil {
		t.Fatalf("publish migration fixture: %v", err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	for range 100 {
		h.advance()
		if err := h.svc.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		current, err := h.svc.GetRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == StateSuccessful {
			if len(current.Children) != 2 {
				t.Fatalf("mapped items = %d, want 2", len(current.Children))
			}
			for _, item := range current.Children {
				if item.State != StateSuccessful {
					t.Fatalf("item %s = %s", item.Key, item.State)
				}
			}
			break
		}
		if current.State == StateFailed {
			t.Fatalf("migration fixture failed: %+v", current.Nodes)
		}
		time.Sleep(time.Millisecond)
	}
	finished, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != StateSuccessful {
		t.Fatalf("migration fixture did not finish: %s", finished.State)
	}
	agent.mu.Lock()
	prompts := strings.Join(agent.prompts, "\n")
	agent.mu.Unlock()
	if strings.Count(prompts, "Review only the declared implementation diff") != 4 {
		t.Fatalf("review prompts = %q", prompts)
	}
	if !strings.Contains(prompts, "Use only this item and the shared migration guidance") {
		t.Fatalf("implementer input boundary missing: %q", prompts)
	}
}

func TestDiagnosticPartitionFixtureDoesNotRepeatDiscovery(t *testing.T) {
	h := newHarness(t)
	directory := filepath.Dir(h.path)
	if _, err := h.svc.Publish(context.Background(), fixtureForDirectory(t, readMigrationFixture(t, "migration-item.yaml"), directory)); err != nil {
		t.Fatalf("publish item fixture: %v", err)
	}
	if _, err := h.svc.Validate(context.Background(), fixtureForDirectory(t, readMigrationFixture(t, "diagnostic-partitions.yaml"), directory)); err != nil {
		t.Fatalf("validate diagnostic fixture: %v", err)
	}
}
