package state

import (
	"database/sql"
	"fmt"
)

// WorkflowArtifact is the immutable metadata row for one artifact
// produced by a node attempt. The payload itself lives in the on-disk
// content-addressed store keyed by ContentHash; PayloadDeleted marks a
// row whose payload has been dropped by retention cleanup (metadata
// survives for audit).
type WorkflowArtifact struct {
	ID             string
	RunID          string
	NodeID         string
	AttemptID      int64
	Name           string
	Kind           string
	ContentHash    string
	SizeBytes      int64
	CreatedAt      int64
	ExpiresAt      int64 // 0 = never expires
	PayloadDeleted bool
}

// InsertWorkflowArtifact persists one immutable artifact metadata row.
// Artifacts are write-once; there is no update path (producer
// immutability). ExpiresAt of 0 stores NULL (never expires).
func (d *DB) InsertWorkflowArtifact(a WorkflowArtifact) error {
	var expires interface{}
	if a.ExpiresAt > 0 {
		expires = a.ExpiresAt
	}
	_, err := d.db.Exec(`
		INSERT INTO workflow_artifact
			(id, run_id, node_id, attempt_id, name, kind, content_hash, size_bytes, created_at, expires_at, payload_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		a.ID, a.RunID, a.NodeID, a.AttemptID, a.Name, a.Kind, a.ContentHash, a.SizeBytes, a.CreatedAt, expires)
	if err != nil {
		return fmt.Errorf("inserting workflow artifact: %w", err)
	}
	return nil
}

// ListWorkflowArtifacts returns every artifact for a run, ordered by
// node then creation time, so the UI can group them by producing node.
func (d *DB) ListWorkflowArtifacts(runID string) ([]WorkflowArtifact, error) {
	rows, err := d.db.Query(`
		SELECT id, run_id, node_id, attempt_id, name, kind, content_hash, size_bytes,
		       created_at, COALESCE(expires_at, 0), payload_deleted
		FROM workflow_artifact WHERE run_id = ?
		ORDER BY node_id, created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing workflow artifacts: %w", err)
	}
	defer rows.Close()
	return scanWorkflowArtifacts(rows)
}

// GetWorkflowArtifact returns one artifact metadata row by ID.
func (d *DB) GetWorkflowArtifact(id string) (*WorkflowArtifact, error) {
	var a WorkflowArtifact
	err := d.db.QueryRow(`
		SELECT id, run_id, node_id, attempt_id, name, kind, content_hash, size_bytes,
		       created_at, COALESCE(expires_at, 0), payload_deleted
		FROM workflow_artifact WHERE id = ?`, id).Scan(
		&a.ID, &a.RunID, &a.NodeID, &a.AttemptID, &a.Name, &a.Kind, &a.ContentHash,
		&a.SizeBytes, &a.CreatedAt, &a.ExpiresAt, &a.PayloadDeleted)
	if err != nil {
		return nil, fmt.Errorf("getting workflow artifact: %w", err)
	}
	return &a, nil
}

// ExpiredWorkflowArtifactHashes returns content hashes whose payload is
// eligible for cleanup at time `now`: every non-deleted metadata row
// referencing the hash has expired. A hash still referenced by any
// non-expired (or never-expiring) row is withheld, so shared payloads
// survive until the last retention window closes. Never-expiring rows
// (expires_at IS NULL) keep their payload indefinitely.
func (d *DB) ExpiredWorkflowArtifactHashes(now int64) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT content_hash FROM workflow_artifact
		WHERE payload_deleted = 0
		GROUP BY content_hash
		HAVING max(CASE WHEN expires_at IS NULL OR expires_at > ? THEN 1 ELSE 0 END) = 0`, now)
	if err != nil {
		return nil, fmt.Errorf("finding expired artifact hashes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("scanning expired artifact hash: %w", err)
		}
		out = append(out, hash)
	}
	return out, rows.Err()
}

// MarkWorkflowArtifactPayloadDeleted flips payload_deleted for every row
// referencing a content hash. Called after the on-disk blob is removed.
func (d *DB) MarkWorkflowArtifactPayloadDeleted(hash string) error {
	_, err := d.db.Exec(`UPDATE workflow_artifact SET payload_deleted = 1 WHERE content_hash = ?`, hash)
	if err != nil {
		return fmt.Errorf("marking artifact payload deleted: %w", err)
	}
	return nil
}

func scanWorkflowArtifacts(rows *sql.Rows) ([]WorkflowArtifact, error) {
	var out []WorkflowArtifact
	for rows.Next() {
		var a WorkflowArtifact
		if err := rows.Scan(&a.ID, &a.RunID, &a.NodeID, &a.AttemptID, &a.Name, &a.Kind,
			&a.ContentHash, &a.SizeBytes, &a.CreatedAt, &a.ExpiresAt, &a.PayloadDeleted); err != nil {
			return nil, fmt.Errorf("scanning workflow artifact: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
