package workflows

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func (h *harness) storeTestArtifact(t *testing.T, payload string, retentionDays int) (string, Artifact) {
	t.Helper()
	definition := Definition{
		ID: "art", Name: "Artifacts", Version: "1", Concurrency: 1, RetentionDays: retentionDays,
		Triggers: []Trigger{{ID: "manual", Type: TriggerManual}},
		Nodes:    []Node{{ID: "emit", Name: "Emit", Type: "approval"}},
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	version, err := h.svc.PublishJSON(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Approve(t.Context(), run.ID, "emit"); err != nil {
		t.Fatal(err)
	}
	h.svc.storeArtifact(run.ID, "emit", 1, "historical", KindJSON, []byte(payload), retentionDays)
	artifacts, err := h.svc.ListArtifacts(t.Context(), run.ID)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("stored artifacts = %+v (%v)", artifacts, err)
	}
	return run.ID, artifacts[0]
}

func TestArtifactContentAddressingRetentionAndRestart(t *testing.T) {
	h := newHarness(t)
	runID, artifact := h.storeTestArtifact(t, `{"ok":true}`, 7)
	if artifact.ContentHash == "" || artifact.ExpiresAt != retentionExpiry(h.now, 7) {
		t.Fatalf("artifact metadata = %+v", artifact)
	}
	_, payload, err := h.svc.DownloadArtifact(t.Context(), runID, artifact.ID)
	if err != nil || artifact.ContentHash != Hash(payload) {
		t.Fatalf("artifact payload = %s (%v)", payload, err)
	}
	h.restart()
	after, err := h.svc.ListArtifacts(t.Context(), runID)
	if err != nil || len(after) != 1 || after[0].ContentHash != artifact.ContentHash {
		t.Fatalf("artifact did not survive restart: %+v (%v)", after, err)
	}
}

func TestArtifactDedupAndCleanup(t *testing.T) {
	h := newHarness(t)
	_, first := h.storeTestArtifact(t, `{"same":true}`, 1)
	_, second := h.storeTestArtifact(t, `{"same":true}`, 1)
	if first.ContentHash != second.ContentHash || first.ID == second.ID {
		t.Fatalf("artifact dedup metadata = %+v %+v", first, second)
	}
	h.now = h.now.Add(48 * time.Hour)
	removed, err := h.svc.CleanupExpiredPayloads(t.Context())
	if err != nil || removed != 1 {
		t.Fatalf("cleanup removed %d: %v", removed, err)
	}
	_, _, err = h.svc.DownloadArtifact(t.Context(), first.RunID, first.ID)
	if !errors.Is(err, ErrPayloadMissing) {
		t.Fatalf("download after cleanup = %v", err)
	}
}

func TestArtifactDownloadErrors(t *testing.T) {
	h := newHarness(t)
	runID, artifact := h.storeTestArtifact(t, `{"ok":true}`, 1)
	if _, _, err := h.svc.DownloadArtifact(t.Context(), runID, "missing"); err == nil {
		t.Fatal("missing artifact downloaded")
	}
	h.svc.blobs = nil
	if _, _, err := h.svc.DownloadArtifact(t.Context(), runID, artifact.ID); !errors.Is(err, ErrPayloadMissing) {
		t.Fatalf("download without blob store = %v", err)
	}
}
