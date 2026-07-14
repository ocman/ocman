package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// artifactWorkflow publishes a single command node that emits the given
// stdout via a declared text collector, plus optional secrets/retention.
func (h *harness) publishArtifactCommand(t *testing.T, stdout string, secrets []SecretRef, retentionDays int) Version {
	t.Helper()
	def := Definition{
		ID: "art", Name: "Artifacts", Version: "1", Concurrency: 1, Directory: h.t.TempDir(),
		RetentionDays: retentionDays,
		Secrets:       secrets,
		Triggers:      []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes: []Node{{
			ID: "emit", Name: "Emit", Type: "command",
			Command:    []string{"/bin/sh", "-c", "printf '%s' \"" + stdout + "\""},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}},
			Outputs:    []Collector{{Name: "log", Type: "text"}},
		}},
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	version, err := h.svc.PublishJSON(context.Background(), raw)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return version
}

func TestArtifactContentAddressedAndImmutable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	version := h.publishArtifactCommand(t, "produced output", nil, 0)
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run = waitForRun(t, h.svc, run.ID, StateSuccessful)

	artifacts, err := h.svc.ListArtifacts(ctx, run.ID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %+v", len(artifacts), artifacts)
	}
	art := artifacts[0]
	if art.Name != "log" || art.Kind != KindText {
		t.Fatalf("unexpected artifact metadata: %+v", art)
	}
	// The stored payload is the collector JSON value.
	if art.ContentHash == "" || art.Size == 0 {
		t.Fatalf("artifact missing content address/size: %+v", art)
	}
	if art.ExpiresAt != retentionExpiry(h.now, 0) {
		t.Fatalf("default retention not applied: %d", art.ExpiresAt)
	}
	_, payload, err := h.svc.DownloadArtifact(ctx, art.ID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if art.ContentHash != Hash(payload) {
		t.Fatalf("content hash %q does not address payload %q", art.ContentHash, payload)
	}
	if !strings.Contains(string(payload), "produced output") {
		t.Fatalf("payload does not contain output: %q", payload)
	}
}

func TestArtifactDedupIndependentRetention(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Two separate runs produce identical payloads → one blob, two rows.
	v1 := h.publishArtifactCommand(t, "same bytes", nil, 0)
	r1, err := h.svc.Start(ctx, v1.ID)
	if err != nil {
		t.Fatalf("start r1: %v", err)
	}
	waitForRun(t, h.svc, r1.ID, StateSuccessful)

	v2 := h.publishArtifactCommand(t, "same bytes", nil, 7)
	r2, err := h.svc.Start(ctx, v2.ID)
	if err != nil {
		t.Fatalf("start r2: %v", err)
	}
	waitForRun(t, h.svc, r2.ID, StateSuccessful)

	a1, _ := h.svc.ListArtifacts(ctx, r1.ID)
	a2, _ := h.svc.ListArtifacts(ctx, r2.ID)
	if len(a1) != 1 || len(a2) != 1 {
		t.Fatalf("expected one artifact per run")
	}
	if a1[0].ContentHash != a2[0].ContentHash {
		t.Fatalf("identical payloads did not dedup: %q vs %q", a1[0].ContentHash, a2[0].ContentHash)
	}
	// Independent retention: default 30d vs override 7d.
	if a1[0].ExpiresAt != retentionExpiry(h.now, 0) || a2[0].ExpiresAt != retentionExpiry(h.now, 7) {
		t.Fatalf("retention not independent: %d vs %d", a1[0].ExpiresAt, a2[0].ExpiresAt)
	}
	if a1[0].ID == a2[0].ID {
		t.Fatalf("shared metadata id for deduped payloads")
	}
}

func TestArtifactRedactsResolvedSecrets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.secrets["API_TOKEN"] = "sup3r-s3cret"
	version := h.publishArtifactCommand(t, "token is $API_TOKEN done",
		[]SecretRef{{Name: "token", Env: "API_TOKEN"}}, 0)
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run = waitForRun(t, h.svc, run.ID, StateSuccessful)

	// The command resolved the secret from env (so it printed the real
	// value) but stdout must be redacted before persistence.
	attempt := run.Nodes[0].Attempts[0]
	if strings.Contains(attempt.Stdout, "sup3r-s3cret") {
		t.Fatalf("secret leaked into persisted stdout: %q", attempt.Stdout)
	}
	if !strings.Contains(attempt.Stdout, redactionMarker) {
		t.Fatalf("stdout not redacted: %q", attempt.Stdout)
	}
	// And the published artifact payload must be redacted too.
	artifacts, _ := h.svc.ListArtifacts(ctx, run.ID)
	_, payload, err := h.svc.DownloadArtifact(ctx, artifacts[0].ID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if strings.Contains(string(payload), "sup3r-s3cret") {
		t.Fatalf("secret leaked into artifact payload: %q", payload)
	}
}

func TestArtifactCleanupPreservesMetadataAndMissingPayload(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	version := h.publishArtifactCommand(t, "cleanup me", nil, 1) // 1-day retention
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run = waitForRun(t, h.svc, run.ID, StateSuccessful)
	artifacts, _ := h.svc.ListArtifacts(ctx, run.ID)
	art := artifacts[0]

	// Before expiry: cleanup removes nothing.
	removed, err := h.svc.CleanupExpiredPayloads(ctx)
	if err != nil {
		t.Fatalf("cleanup before expiry: %v", err)
	}
	if removed != 0 {
		t.Fatalf("cleaned payload before expiry: %d", removed)
	}

	// Advance past the 1-day window.
	h.now = h.now.Add(48 * time.Hour)
	removed, err = h.svc.CleanupExpiredPayloads(ctx)
	if err != nil {
		t.Fatalf("cleanup after expiry: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 payload cleaned, got %d", removed)
	}

	// Metadata survives; payload is gone.
	after, err := h.svc.ListArtifacts(ctx, run.ID)
	if err != nil {
		t.Fatalf("list after cleanup: %v", err)
	}
	if len(after) != 1 || after[0].Name != art.Name || after[0].ContentHash != art.ContentHash {
		t.Fatalf("metadata lost after cleanup: %+v", after)
	}
	if after[0].PayloadAvailable {
		t.Fatalf("payload still marked available after cleanup")
	}
	// Downloading a cleaned payload reports the missing state, not a crash.
	_, _, err = h.svc.DownloadArtifact(ctx, art.ID)
	if !errors.Is(err, ErrPayloadMissing) {
		t.Fatalf("expected ErrPayloadMissing after cleanup, got %v", err)
	}
	// Blob file is actually gone from disk.
	if h.blobs.Has(art.ContentHash) {
		t.Fatalf("blob not removed from content store")
	}
}

func TestArtifactAgentRedactionAndPublication(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.secrets["OPENAI_KEY"] = "leaky-key"
	h.agent.results["session-1"] = AgentResult{State: "idle", Outputs: map[string]json.RawMessage{
		"message": json.RawMessage(`"final answer using leaky-key here"`),
	}}
	def := Definition{
		ID: "agentart", Name: "AgentArt", Version: "1", Concurrency: 1,
		Secrets:  []SecretRef{{Name: "key", Env: "OPENAI_KEY"}},
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes: []Node{{ID: "impl", Name: "Impl", Type: "agent", Agent: &AgentConfig{
			Platform: "test", Directory: "/repo", Prompt: "do it",
			Collectors: []Collector{{Name: "message", Type: "final-message"}},
		}}},
	}
	raw, _ := json.Marshal(def)
	version, err := h.svc.PublishJSON(ctx, raw)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	run = waitForRun(t, h.svc, run.ID, StateSuccessful)

	attempt := run.Nodes[0].Attempts[0]
	if strings.Contains(string(attempt.Outputs["message"]), "leaky-key") {
		t.Fatalf("secret leaked into agent output: %s", attempt.Outputs["message"])
	}
	artifacts, _ := h.svc.ListArtifacts(ctx, run.ID)
	if len(artifacts) != 1 || artifacts[0].Kind != KindText {
		t.Fatalf("expected one text artifact, got %+v", artifacts)
	}
	_, payload, err := h.svc.DownloadArtifact(ctx, artifacts[0].ID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if strings.Contains(string(payload), "leaky-key") {
		t.Fatalf("secret leaked into agent artifact: %q", payload)
	}
}

func TestConsumableArtifactsAreDeclaredInputsOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := h.t.TempDir()
	// a -> c, b (independent) also produces an artifact. c depends only
	// on a, so c may consume a's artifact but NOT b's.
	def := Definition{
		ID: "consume", Name: "Consume", Version: "1", Concurrency: 3, Directory: dir,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes: []Node{
			{ID: "a", Name: "A", Type: "command", Command: []string{"/bin/sh", "-c", "printf a"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}}, Outputs: []Collector{{Name: "out", Type: "text"}}},
			{ID: "b", Name: "B", Type: "command", Command: []string{"/bin/sh", "-c", "printf b"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}}, Outputs: []Collector{{Name: "out", Type: "text"}}},
			{ID: "c", Name: "C", Type: "command", Command: []string{"/bin/sh", "-c", "printf c"}, Permission: []PermissionRule{{Permission: "bash", Pattern: "/bin/sh -c *", Action: "allow"}}, Outputs: []Collector{{Name: "out", Type: "text"}}},
		},
		Dependencies: []Dependency{{From: "a", To: "c"}},
	}
	raw, _ := json.Marshal(def)
	version, err := h.svc.PublishJSON(ctx, raw)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForRun(t, h.svc, run.ID, StateSuccessful)

	consumable, err := h.svc.ConsumableArtifacts(ctx, run.ID, "c")
	if err != nil {
		t.Fatalf("consumable: %v", err)
	}
	if len(consumable) != 1 || consumable[0].NodeID != "a" {
		t.Fatalf("c should only see a's artifact, got %+v", consumable)
	}
	// A node with no upstream sees nothing.
	rootConsumable, err := h.svc.ConsumableArtifacts(ctx, run.ID, "a")
	if err != nil {
		t.Fatalf("consumable a: %v", err)
	}
	if len(rootConsumable) != 0 {
		t.Fatalf("root node a should have no declared inputs, got %+v", rootConsumable)
	}
}

func TestArtifactSurvivesRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	version := h.publishArtifactCommand(t, "durable", nil, 0)
	run, err := h.svc.Start(ctx, version.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForRun(t, h.svc, run.ID, StateSuccessful)
	before, _ := h.svc.ListArtifacts(ctx, run.ID)

	h.restart()
	after, err := h.svc.ListArtifacts(ctx, run.ID)
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(after) != 1 || after[0].ID != before[0].ID || after[0].ContentHash != before[0].ContentHash {
		t.Fatalf("artifact metadata did not survive restart: %+v vs %+v", before, after)
	}
	// Payload is still downloadable from the on-disk store after restart.
	_, payload, err := h.svc.DownloadArtifact(ctx, after[0].ID)
	if err != nil || !strings.Contains(string(payload), "durable") {
		t.Fatalf("payload not durable across restart: %v, %q", err, payload)
	}
}
