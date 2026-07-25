package dagu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

func compileSpec(t *testing.T, definition workflows.Definition, options CompileOptions) string {
	t.Helper()
	if options.RunID == "" {
		options.RunID = "run-1"
	}
	compiled, err := Compile(definition, options)
	if err != nil {
		t.Fatal(err)
	}
	return string(compiled.Spec)
}

func TestCompileCommandWorkflow(t *testing.T) {
	want := `working_dir: /repo
retry_policy:
    limit: 0
steps:
    - name: build
      command: printf %s 'hello world'
      env:
        MODE: test
      output: OUT_BUILD
    - name: ship
      command: ./ship
      output: OUT_SHIP
      depends:
        - build
`
	if spec := compileSpec(t, commandDefinition(), CompileOptions{}); spec != want {
		t.Fatalf("spec =\n%s\nwant\n%s", spec, want)
	}
}

// Dagu defaults to three automatic DAG retries. On agent nodes that
// silently triples LLM spend per failure, so every spec must disable it.
func TestCompileAlwaysDisablesAutoRetry(t *testing.T) {
	if spec := compileSpec(t, commandDefinition(), CompileOptions{}); !strings.Contains(spec, "retry_policy:\n    limit: 0") {
		t.Fatalf("spec does not disable auto-retry:\n%s", spec)
	}
}

// The mirror joins dagu nodes back to ocman rows on the step name, so it
// must carry the node ID even when a display name exists.
func TestCompileNamesStepsByNodeID(t *testing.T) {
	spec := compileSpec(t, commandDefinition(), CompileOptions{})
	if !strings.Contains(spec, "- name: build") || strings.Contains(spec, "name: Build") {
		t.Fatalf("steps are not keyed by node ID:\n%s", spec)
	}
}

func TestCompileConcurrencyBoundsActiveSteps(t *testing.T) {
	definition := commandDefinition()
	definition.Concurrency = 3
	if spec := compileSpec(t, definition, CompileOptions{}); !strings.Contains(spec, "max_active_steps: 3") {
		t.Fatalf("spec = %s", spec)
	}
}

func TestCompileTranslatesNodeOutputReferences(t *testing.T) {
	definition := commandDefinition()
	definition.Nodes[1].Command = []string{"./ship", "${nodes.build.output.tag}"}
	definition.Nodes[1].Environment = map[string]string{"ALL": "${nodes.build.output}"}
	spec := compileSpec(t, definition, CompileOptions{})
	for _, want := range []string{"./ship '${OUT_BUILD.tag}'", "ALL: ${OUT_BUILD}"} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec missing %q:\n%s", want, spec)
		}
	}
}

func TestCompileRejectsBadReferences(t *testing.T) {
	for name, test := range map[string]struct {
		command []string
		want    string
	}{
		// Dagu captures stdout only, so envelope fields cannot resolve.
		"envelope field": {[]string{"./ship", "${nodes.build.status}"}, "only the output channel"},
		"unknown node":   {[]string{"./ship", "${nodes.nope.output}"}, "does not identify a dependency"},
		// Dagu resolves object fields but leaves an array index
		// unsubstituted, which reaches the shell as a bad substitution.
		"array index": {[]string{"./ship", "${nodes.build.output.list.0.id}"}, "indexes an array"},
		// build depends on nothing, so it cannot see ship.
		"not a dependency": {[]string{"./ship", "${nodes.ship.output}"}, "does not identify a dependency"},
		"malformed":        {[]string{"./ship", "${nodes.build"}, "malformed"},
	} {
		t.Run(name, func(t *testing.T) {
			definition := commandDefinition()
			target := 1
			if name == "not a dependency" {
				target = 0
			}
			definition.Nodes[target].Command = test.command
			if _, err := Compile(definition, CompileOptions{RunID: "run-1"}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// Folding node IDs to shell identifiers can collide, and a collision
// would cross-wire two nodes' results instead of failing.
func TestCompileRejectsVariableCollision(t *testing.T) {
	definition := commandDefinition()
	definition.Nodes[0].ID = "a-b"
	definition.Nodes[1].ID = "a.b"
	definition.Dependencies = nil
	if _, err := Compile(definition, CompileOptions{RunID: "run-1"}); err == nil || !strings.Contains(err.Error(), "OUT_A_B") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompileAgentAndApprovalUseShim(t *testing.T) {
	definition := commandDefinition()
	definition.Nodes[1] = workflows.Node{ID: "review", Name: "Review", Type: "agent",
		Agent: &workflows.AgentConfig{Directory: "/repo", Prompt: "check ${nodes.build.output}"}}
	definition.Nodes = append(definition.Nodes, workflows.Node{ID: "gate", Name: "Gate", Type: "approval"})
	definition.Dependencies = []workflows.Dependency{{From: "build", To: "review"}, {From: "review", To: "gate"}}
	spec := compileSpec(t, definition, CompileOptions{Shim: "/usr/bin/ocman"})
	for _, want := range []string{
		"command: /usr/bin/ocman workflow-step agent --run run-1 --node review",
		"command: /usr/bin/ocman workflow-step approval --run run-1 --node gate",
		`OCMAN_UPSTREAM: '{"build": ${OUT_BUILD}}'`,
		`OCMAN_UPSTREAM: '{"build": ${OUT_BUILD}, "review": ${OUT_REVIEW}}'`,
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec missing %q:\n%s", want, spec)
		}
	}
	// The prompt is resolved by the shim from ocman's database; it must
	// never reach the spec or dagu's on-disk step logs.
	if strings.Contains(spec, "check ") {
		t.Errorf("prompt leaked into spec:\n%s", spec)
	}
}

// Dagu evaluates a precondition as a bare command: exit 0 proceeds,
// non-zero skips. Ocman owns the CEL evaluator, so the condition
// delegates to the shim rather than being translated to shell.
func TestCompileConditionalDependencyBecomesPrecondition(t *testing.T) {
	definition := commandDefinition()
	definition.Dependencies[0].Condition = "nodes.build.output.ok == true"
	spec := compileSpec(t, definition, CompileOptions{Shim: "ocman"})
	want := "condition: ocman workflow-step condition --run run-1 --node ship --from build"
	if !strings.Contains(spec, want) {
		t.Fatalf("spec missing %q:\n%s", want, spec)
	}
	// The CEL text belongs to ocman and must not leak into the spec.
	if strings.Contains(spec, "== true") {
		t.Errorf("condition expression leaked into spec:\n%s", spec)
	}
}

func mapDefinition() (workflows.Definition, workflows.Definition) {
	child := workflows.Definition{
		ID: "item", Name: "Item", Directory: "/repo",
		Triggers: []workflows.Trigger{{ID: "manual", Type: workflows.TriggerManual}},
		Nodes: []workflows.Node{
			{ID: "implement", Name: "Implement", Type: "agent",
				Agent: &workflows.AgentConfig{Directory: "/repo", Prompt: "fix ${item.path}"}},
		},
	}
	parent := workflows.Definition{
		ID: "campaign", Name: "Campaign", Directory: "/repo", Concurrency: 4,
		Triggers: []workflows.Trigger{{ID: "manual", Type: workflows.TriggerManual}},
		Nodes: []workflows.Node{
			{ID: "discover", Name: "Discover", Type: "command", Command: []string{"./discover"}},
			{ID: "items", Name: "Items", Type: "map", Map: &workflows.MapConfig{
				Source: "${nodes.discover.output}", Key: "id", Join: "collect",
				Subworkflow: workflows.SubworkflowRef{WorkflowID: "item"}, VersionID: "ver-7"}},
			{ID: "collect", Name: "Collect", Type: "join", Join: &workflows.JoinConfig{Policy: workflows.JoinAllSuccess}},
		},
		Dependencies: []workflows.Dependency{{From: "discover", To: "items"}, {From: "items", To: "collect"}},
	}
	return parent, child
}

func TestCompileMapFansOutToPinnedChildDAG(t *testing.T) {
	parent, child := mapDefinition()
	compiled, err := Compile(parent, CompileOptions{RunID: "run-1", Shim: "ocman",
		ResolveVersion: func(string) (workflows.Definition, error) { return child, nil }})
	if err != nil {
		t.Fatal(err)
	}
	name := SafeDAGName("item", "ver-7")
	for _, want := range []string{
		"action: dag.run",
		"dag: " + name,
		"items: ${OUT_DISCOVER}",
		"max_concurrent: 4",
		"ocman_item_key: ${ITEM.id}",
		"ocman_parent_run: run-1",
		"output: OUT_ITEMS",
	} {
		if !strings.Contains(string(compiled.Spec), want) {
			t.Errorf("parent spec missing %q:\n%s", want, compiled.Spec)
		}
	}
	spec, ok := compiled.Children[name]
	if !ok {
		t.Fatalf("child %q not compiled; got %v", name, compiled.Children)
	}
	// One compiled child serves every item of every run, so the parent
	// run and stable item key must arrive as parameters.
	for _, want := range []string{"ocman_parent_run: \"\"", "OCMAN_ITEM_KEY: ${ocman_item_key}", "OCMAN_PARENT_RUN: ${ocman_parent_run}"} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("child spec missing %q:\n%s", want, spec)
		}
	}
	if strings.Contains(string(spec), "--run run-1") {
		t.Errorf("child spec baked in the parent run ID:\n%s", spec)
	}
}

// A failed join policy must fail the run, not skip it, so the join is a
// command rather than a precondition.
func TestCompileJoinConsumesMapAggregate(t *testing.T) {
	parent, child := mapDefinition()
	compiled, err := Compile(parent, CompileOptions{RunID: "run-1", Shim: "ocman",
		ResolveVersion: func(string) (workflows.Definition, error) { return child, nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"command: ocman workflow-step join --run run-1 --node collect",
		"OCMAN_MAP_RESULT: ${OUT_ITEMS}",
	} {
		if !strings.Contains(string(compiled.Spec), want) {
			t.Errorf("spec missing %q:\n%s", want, compiled.Spec)
		}
	}
}

func TestCompileMapRequiresPinAndResolver(t *testing.T) {
	parent, child := mapDefinition()
	resolver := func(string) (workflows.Definition, error) { return child, nil }
	for name, test := range map[string]struct {
		mutate func(*workflows.Definition)
		option CompileOptions
		want   string
	}{
		"no resolver": {func(*workflows.Definition) {}, CompileOptions{RunID: "run-1"}, "version resolver"},
		"no version": {func(d *workflows.Definition) { d.Nodes[1].Map.VersionID = "" },
			CompileOptions{RunID: "run-1", ResolveVersion: resolver}, "pinned subworkflow version"},
		"no key": {func(d *workflows.Definition) { d.Nodes[1].Map.Key = "" },
			CompileOptions{RunID: "run-1", ResolveVersion: resolver}, "stable item key"},
		"orphan join": {func(d *workflows.Definition) { d.Nodes[1].Map.Join = "" },
			CompileOptions{RunID: "run-1", ResolveVersion: resolver}, "not referenced by any map"},
	} {
		t.Run(name, func(t *testing.T) {
			definition, _ := mapDefinition()
			test.mutate(&definition)
			if _, err := Compile(definition, test.option); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	_ = parent
}

// Sanitizing alone can collide, so the digest of the exact version ID
// always distinguishes two names.
func TestSafeDAGNameIsSafeAndCollisionFree(t *testing.T) {
	first := SafeDAGName("My Repo/Flow", "ver-1")
	second := SafeDAGName("My:Repo Flow", "ver-1")
	third := SafeDAGName("My Repo/Flow", "ver-2")
	if first == second || first == third {
		t.Fatalf("names collide: %q %q %q", first, second, third)
	}
	for _, name := range []string{first, second, third} {
		if strings.ContainsAny(name, "/: .") || name != strings.ToLower(name) {
			t.Errorf("unsafe name %q", name)
		}
	}
	if SafeDAGName("My Repo/Flow", "ver-1") != first {
		t.Error("name is not deterministic")
	}
}

func TestCompileRejectsUnsupportedFeatures(t *testing.T) {
	for name, mutate := range map[string]func(*workflows.Definition){
		"secrets":   func(d *workflows.Definition) { d.Secrets = []workflows.SecretRef{{Name: "t", Env: "T"}} },
		"pools":     func(d *workflows.Definition) { d.Pools = []workflows.Pool{{Name: "p", Capacity: 1}} },
		"workspace": func(d *workflows.Definition) { d.Workspace = &workflows.WorkspaceConfig{Shards: 1} },
		"limits":    func(d *workflows.Definition) { d.Limits = &workflows.Limits{MaxTokens: 1} },
		"fail fast": func(d *workflows.Definition) { d.FailFast = true },
		"repeat":    func(d *workflows.Definition) { d.Nodes[0].Repeat = &workflows.RepeatConfig{Until: "x", MaxAttempts: 2} },
		"lease":     func(d *workflows.Definition) { d.Nodes[0].Lease = &workflows.LeaseConfig{Mode: "exclusive"} },
		"resources": func(d *workflows.Definition) {
			d.Nodes[0].Resources = []workflows.ResourceRequest{{Pool: "p", Units: 1}}
		},
		"unknown type": func(d *workflows.Definition) { d.Nodes[0].Type = "wat" },
	} {
		t.Run(name, func(t *testing.T) {
			definition := commandDefinition()
			mutate(&definition)
			if _, err := Compile(definition, CompileOptions{RunID: "run-1"}); err == nil {
				t.Fatalf("%s compiled without error", name)
			}
		})
	}
}

// Dagu resolves depends by name at run time, so an unknown endpoint
// would surface as a failure long after ocman reported the run started.
func TestCompileRejectsUnknownDependencyEndpoint(t *testing.T) {
	definition := commandDefinition()
	definition.Dependencies = []workflows.Dependency{{From: "build", To: "ghost"}}
	if _, err := Compile(definition, CompileOptions{RunID: "run-1"}); err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Fatalf("error = %v", err)
	}
}

// Ocman owns triggering, so a compiled spec never carries a schedule no
// matter what triggers the definition declares.
func TestCompileNeverEmitsSchedule(t *testing.T) {
	definition := commandDefinition()
	definition.Triggers = append(definition.Triggers, workflows.Trigger{ID: "nightly", Type: workflows.TriggerCron, Cron: "0 3 * * *"})
	spec := compileSpec(t, definition, CompileOptions{})
	if strings.Contains(spec, "schedule") || strings.Contains(spec, "0 3 * * *") {
		t.Fatalf("spec carries a schedule:\n%s", spec)
	}
}

func TestCompileRequiresRunID(t *testing.T) {
	if _, err := Compile(commandDefinition(), CompileOptions{}); err == nil {
		t.Fatal("compiled without a run ID")
	}
}

// A golden string only proves what ocman thinks. This proves dagu itself
// accepts the spec, honours depends, resolves translated output
// references, skips on a failing precondition, and fans a map out to the
// pinned child DAG with the stable item key.
func TestCompiledSpecRunsUnderRealDagu(t *testing.T) {
	binary, err := exec.LookPath("dagu")
	if err != nil {
		t.Skip("dagu not installed")
	}
	home := t.TempDir()
	dags := filepath.Join(home, "dags")
	if err := os.MkdirAll(dags, 0700); err != nil {
		t.Fatal(err)
	}
	child := workflows.Definition{
		ID: "item", Name: "Item", Directory: home,
		Nodes: []workflows.Node{{ID: "work", Name: "Work", Type: "approval"}},
	}
	definition := workflows.Definition{
		ID: "spike", Name: "Spike", Directory: home, Concurrency: 2,
		Nodes: []workflows.Node{
			{ID: "emit", Name: "Emit", Type: "command",
				Command: []string{"sh", "-c", `echo '{"first":"a","items":[{"id":"a"},{"id":"b"}]}'`}},
			{ID: "use", Name: "Use", Type: "command",
				Command: []string{"sh", "-c", `echo "first=${nodes.emit.output.first}"`}},
			{ID: "items", Name: "Items", Type: "map", Map: &workflows.MapConfig{
				Source: "${nodes.emit.output.items}", Key: "id", Join: "collect",
				Subworkflow: workflows.SubworkflowRef{WorkflowID: "item"}, VersionID: "ver-7"}},
			{ID: "collect", Name: "Collect", Type: "join",
				Join: &workflows.JoinConfig{Policy: workflows.JoinAllSuccess}},
			{ID: "gated", Name: "Gated", Type: "command", Command: []string{"echo", "GATED-RAN"}},
		},
		Dependencies: []workflows.Dependency{
			{From: "emit", To: "use"}, {From: "emit", To: "items"}, {From: "items", To: "collect"},
			// /bin/false exits non-zero, so this edge must skip.
			{From: "use", To: "gated", Condition: "never"},
		},
	}
	compiled, err := Compile(definition, CompileOptions{RunID: "run-1", Shim: "/bin/echo",
		ResolveVersion: func(string) (workflows.Definition, error) { return child, nil }})
	if err != nil {
		t.Fatal(err)
	}
	for name, spec := range compiled.Children {
		if err := os.WriteFile(filepath.Join(dags, name+".yaml"), spec, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The condition shim must fail so the gated step is skipped.
	parent := strings.ReplaceAll(string(compiled.Spec),
		"condition: /bin/echo workflow-step condition --run run-1 --node gated --from use",
		"condition: /usr/bin/false")
	path := filepath.Join(dags, "spike.yaml")
	if err := os.WriteFile(path, []byte(parent), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "start", path)
	command.Env = append(os.Environ(), "DAGU_HOME="+home, "DAGU_AUTH_MODE=none")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dagu rejected the compiled spec: %v\n%s", err, output)
	}
	for _, want := range []string{
		"first=a",
		"workflow-step join --run run-1 --node collect",
		"[skipped]",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("dagu output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(string(output), "GATED-RAN") {
		t.Errorf("failing precondition did not skip the step:\n%s", output)
	}
}
