package workflows

import (
	"context"
	"strings"
	"testing"
)

// leafApprovals is a reusable subworkflow: a single approval node with a
// manual trigger, publishable and pinnable on its own.
const leafApprovals = `{
	"id":"leaf",
	"name":"Leaf",
	"version":"1",
	"concurrency":1,
	"triggers":[{"id":"manual","type":"manual"}],
	"nodes":[
		{"id":"check","name":"Check","type":"approval"}
	]
}`

// TestSubworkflowPinsActiveVersionAndInlines publishes a reusable
// subworkflow, then a parent that references it. The parent's stored
// version must inline the subworkflow's nodes (namespaced) and pin the
// exact active revision so a later subworkflow edit cannot alter the
// parent's execution.
func TestSubworkflowPinsActiveVersionAndInlines(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.PublishJSON(ctx, []byte(leafApprovals)); err != nil {
		t.Fatalf("publish leaf: %v", err)
	}
	parent := `{
		"id":"parent","name":"Parent","version":"1","concurrency":1,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[
			{"id":"gate","name":"Gate","type":"approval"},
			{"id":"sub","name":"Sub","type":"subworkflow","subworkflow":{"workflowId":"leaf"}}
		],
		"dependencies":[{"from":"gate","to":"sub"}]
	}`
	version, err := h.svc.PublishJSON(ctx, []byte(parent))
	if err != nil {
		t.Fatalf("publish parent: %v", err)
	}
	// The subworkflow's node is inlined and namespaced under the
	// referencing node id.
	stored, err := h.svc.GetVersion(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, node := range stored.Definition.Nodes {
		names = append(names, node.ID)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "gate") || !strings.Contains(joined, "sub/check") {
		t.Fatalf("subworkflow was not inlined: %v", names)
	}

	// Pinning: edit the subworkflow after the parent was published.
	edited := strings.Replace(leafApprovals, `{"id":"check","name":"Check","type":"approval"}`,
		`{"id":"check","name":"Check","type":"approval"},{"id":"extra","name":"Extra","type":"approval"}`, 1)
	if _, err := h.svc.PublishJSON(ctx, []byte(edited)); err != nil {
		t.Fatalf("edit leaf: %v", err)
	}
	pinned, err := h.svc.GetVersion(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range pinned.Definition.Nodes {
		if strings.Contains(node.ID, "extra") {
			t.Fatalf("parent picked up post-publish subworkflow edit: %v", node.ID)
		}
	}

	// Running the parent drives the inlined subworkflow node through its
	// own approval within the parent run.
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := h.svc.Approve(ctx, run.ID, "gate"); err != nil {
		t.Fatalf("approve gate: %v", err)
	}
	if _, err := h.svc.Approve(ctx, run.ID, "sub/check"); err != nil {
		t.Fatalf("approve inlined node: %v", err)
	}
	done, err := h.svc.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != StateSuccessful {
		t.Fatalf("parent with subworkflow did not complete: %s", done.State)
	}
}

// TestSubworkflowRejectsRecursiveCycles rejects both a direct self
// reference and an indirect A->B->A cycle at publish time.
func TestSubworkflowRejectsRecursiveCycles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Direct: a workflow referencing itself.
	direct := `{
		"id":"self","name":"Self","version":"1","concurrency":1,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[{"id":"loop","name":"Loop","type":"subworkflow","subworkflow":{"workflowId":"self"}}]
	}`
	if _, err := h.svc.PublishJSON(ctx, []byte(direct)); err == nil || !strings.Contains(err.Error(), "recursive subworkflow") {
		t.Fatalf("direct recursion not rejected: %v", err)
	}

	// Indirect: publish A referencing leaf, then republish leaf to
	// reference A. Because leaf's new active version would then form
	// leaf->A->leaf, publishing that leaf edit must be rejected.
	if _, err := h.svc.PublishJSON(ctx, []byte(leafApprovals)); err != nil {
		t.Fatal(err)
	}
	a := `{
		"id":"a","name":"A","version":"1","concurrency":1,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[{"id":"callLeaf","name":"Call","type":"subworkflow","subworkflow":{"workflowId":"leaf"}}]
	}`
	if _, err := h.svc.PublishJSON(ctx, []byte(a)); err != nil {
		t.Fatalf("publish a: %v", err)
	}
	leafCallsA := strings.Replace(leafApprovals,
		`{"id":"check","name":"Check","type":"approval"}`,
		`{"id":"callA","name":"CallA","type":"subworkflow","subworkflow":{"workflowId":"a"}}`, 1)
	if _, err := h.svc.PublishJSON(ctx, []byte(leafCallsA)); err == nil || !strings.Contains(err.Error(), "recursive subworkflow") {
		t.Fatalf("indirect recursion not rejected: %v", err)
	}
}

// TestSubworkflowMissingReference rejects a reference to an unknown or
// unpublished workflow at publish time.
func TestSubworkflowMissingReference(t *testing.T) {
	h := newHarness(t)
	missing := `{
		"id":"parent","name":"Parent","version":"1","concurrency":1,
		"triggers":[{"id":"manual","type":"manual"}],
		"nodes":[{"id":"sub","name":"Sub","type":"subworkflow","subworkflow":{"workflowId":"ghost"}}]
	}`
	if _, err := h.svc.PublishJSON(context.Background(), []byte(missing)); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("missing subworkflow reference not rejected: %v", err)
	}
}
