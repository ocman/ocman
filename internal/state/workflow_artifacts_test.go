package state

import (
	"path/filepath"
	"testing"
)

func artifactTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open state DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Satisfy the workflow_run FK so artifact inserts are valid.
	if _, err := db.db.Exec(`INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at) VALUES ('wf', 'wf', 1, 0, 0)`); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	if _, err := db.db.Exec(`INSERT INTO workflow_version (id, workflow_id, name, revision, metadata_version, definition_json, concurrency, created_at) VALUES ('v', 'wf', 'wf', 1, '1', '{}', 1, 0)`); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := db.db.Exec(`INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at) VALUES ('run', 'wf', 'v', 'active', 0, 0)`); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return db
}

func TestWorkflowArtifactDedupKeepsIndependentMetadata(t *testing.T) {
	db := artifactTestDB(t)
	// Two artifacts share one content hash (identical payload) but have
	// independent metadata rows and independent retention.
	a1 := WorkflowArtifact{ID: "a1", RunID: "run", NodeID: "n1", AttemptID: 1, Name: "out", Kind: "text", ContentHash: "hash", SizeBytes: 5, CreatedAt: 100, ExpiresAt: 200}
	a2 := WorkflowArtifact{ID: "a2", RunID: "run", NodeID: "n2", AttemptID: 2, Name: "out", Kind: "text", ContentHash: "hash", SizeBytes: 5, CreatedAt: 100, ExpiresAt: 5000}
	if err := db.InsertWorkflowArtifact(t.Context(), a1); err != nil {
		t.Fatalf("insert a1: %v", err)
	}
	if err := db.InsertWorkflowArtifact(t.Context(), a2); err != nil {
		t.Fatalf("insert a2: %v", err)
	}
	list, err := db.ListWorkflowArtifacts(t.Context(), "run")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 independent metadata rows, got %d", len(list))
	}

	// At now=300, a1 is expired but a2 is not: the shared hash must NOT
	// be eligible for cleanup yet.
	hashes, err := db.ExpiredWorkflowArtifactHashes(t.Context(), 300)
	if err != nil {
		t.Fatalf("expired at 300: %v", err)
	}
	if len(hashes) != 0 {
		t.Fatalf("shared hash cleaned while still referenced: %v", hashes)
	}

	// At now=6000, both are expired: the hash is now eligible.
	hashes, err = db.ExpiredWorkflowArtifactHashes(t.Context(), 6000)
	if err != nil {
		t.Fatalf("expired at 6000: %v", err)
	}
	if len(hashes) != 1 || hashes[0] != "hash" {
		t.Fatalf("expected shared hash eligible, got %v", hashes)
	}
}

func TestWorkflowArtifactNeverExpiresWhenExpiresZero(t *testing.T) {
	db := artifactTestDB(t)
	if err := db.InsertWorkflowArtifact(t.Context(), WorkflowArtifact{ID: "keep", RunID: "run", NodeID: "n", AttemptID: 1, Name: "out", Kind: "text", ContentHash: "keephash", SizeBytes: 1, CreatedAt: 0, ExpiresAt: 0}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := db.GetWorkflowArtifact(t.Context(), "keep")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ExpiresAt != 0 {
		t.Fatalf("expected ExpiresAt 0 (never), got %d", got.ExpiresAt)
	}
	hashes, err := db.ExpiredWorkflowArtifactHashes(t.Context(), 1<<62)
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if len(hashes) != 0 {
		t.Fatalf("never-expiring artifact was marked eligible: %v", hashes)
	}
}

func TestMarkWorkflowArtifactPayloadDeletedPreservesMetadata(t *testing.T) {
	db := artifactTestDB(t)
	if err := db.InsertWorkflowArtifact(t.Context(), WorkflowArtifact{ID: "gone", RunID: "run", NodeID: "n", AttemptID: 1, Name: "big", Kind: "diagnostics", ContentHash: "h", SizeBytes: 42, CreatedAt: 10, ExpiresAt: 20}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.MarkWorkflowArtifactPayloadDeleted(t.Context(), "h"); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	got, err := db.GetWorkflowArtifact(t.Context(), "gone")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.PayloadDeleted {
		t.Fatalf("expected payload_deleted true")
	}
	// Metadata (name, kind, size, hash) is preserved for audit.
	if got.Name != "big" || got.Kind != "diagnostics" || got.SizeBytes != 42 || got.ContentHash != "h" {
		t.Fatalf("metadata lost after cleanup: %+v", got)
	}
	// A hash whose only rows are already deleted is not re-reported.
	hashes, err := db.ExpiredWorkflowArtifactHashes(t.Context(), 1<<62)
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if len(hashes) != 0 {
		t.Fatalf("already-deleted hash re-reported: %v", hashes)
	}
}
