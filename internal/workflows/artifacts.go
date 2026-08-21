package workflows

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// Artifact kinds. Immutable typed data produced by one node and
// consumed by others (issue #316 acceptance criteria).
const (
	KindJSON        = "json"
	KindText        = "text"
	KindFile        = "file" // file reference
	KindDiff        = "diff"
	KindDiagnostics = "diagnostics"
)

// DefaultRetentionDays is the default payload retention window. Metadata
// is retained indefinitely; only the content-addressed payload is
// cleaned up after this many days (per-workflow overridable).
const DefaultRetentionDays = 30

// SecretRef names a secret the workflow needs at execution time. The
// definition stores only the reference (Name + host Env var), never the
// resolved value. Resolution happens on the owning host during attempt
// startup and resolved values are redacted from logs/artifacts.
type SecretRef struct {
	Name string `json:"name" yaml:"name"`
	Env  string `json:"env" yaml:"env"`
}

// Artifact is the API view of one immutable artifact metadata record.
// PayloadAvailable is false once retention cleanup has dropped the bytes
// (metadata is preserved for audit).
type Artifact struct {
	ID               string `json:"id"`
	RunID            string `json:"runId"`
	NodeID           string `json:"nodeId"`
	AttemptID        int64  `json:"attemptId"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	ContentHash      string `json:"contentHash"`
	Size             int64  `json:"size"`
	CreatedAt        int64  `json:"createdAt"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	PayloadAvailable bool   `json:"payloadAvailable"`
}

func artifactFromState(row state.WorkflowArtifact) Artifact {
	return Artifact{
		ID: row.ID, RunID: row.RunID, NodeID: row.NodeID, AttemptID: row.AttemptID,
		Name: row.Name, Kind: row.Kind, ContentHash: row.ContentHash, Size: row.SizeBytes,
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, PayloadAvailable: !row.PayloadDeleted,
	}
}

// retentionExpiry returns the payload expiry cutoff for an artifact
// created at `created`, given the workflow's retention override in days
// (0 = DefaultRetentionDays). A negative override means "never expire"
// (retain forever), which returns 0.
func retentionExpiry(created time.Time, retentionDays int) int64 {
	if retentionDays < 0 {
		return 0
	}
	days := retentionDays
	if days == 0 {
		days = DefaultRetentionDays
	}
	return created.Add(time.Duration(days) * 24 * time.Hour).UnixMilli()
}

// redactor replaces known secret values with a fixed marker in any
// captured text (logs and artifact payloads). Longest values are
// replaced first so a secret that is a substring of another does not
// leave a partial leak.
type redactor struct {
	values []string
}

const redactionMarker = "***REDACTED***"

// newRedactor builds a redactor from resolved secret values. Empty
// values are ignored (redacting "" would corrupt all output).
func newRedactor(resolved map[string]string) *redactor {
	seen := map[string]bool{}
	var values []string
	for _, value := range resolved {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return &redactor{values: values}
}

// redact replaces every known secret value in s with the marker.
func (r *redactor) redact(s string) string {
	if r == nil || len(r.values) == 0 || s == "" {
		return s
	}
	for _, value := range r.values {
		s = strings.ReplaceAll(s, value, redactionMarker)
	}
	return s
}

// resolvedSecrets resolves every declared secret reference to its host
// value at execution time. Values never leave this map (the definition
// stores references only).
func (s *Service) resolvedSecrets(version Version) map[string]string {
	if len(version.Definition.Secrets) == 0 {
		return nil
	}
	out := make(map[string]string, len(version.Definition.Secrets))
	for _, secret := range version.Definition.Secrets {
		out[secret.Name] = s.resolveSecret(secret.Env)
	}
	return out
}

// runRedactor builds a redactor from a run's resolved secrets.
func (s *Service) runRedactor(version Version) *redactor {
	return newRedactor(s.resolvedSecrets(version))
}

// secretEnv merges resolved secret values into a node's declared
// environment so command nodes receive live credentials without the
// definition ever containing them. Explicit node env wins on conflict.
func (s *Service) secretEnv(version Version, nodeEnv map[string]string) map[string]string {
	resolved := s.resolvedSecrets(version)
	if len(resolved) == 0 {
		return nodeEnv
	}
	merged := make(map[string]string, len(resolved)+len(nodeEnv))
	for _, secret := range version.Definition.Secrets {
		if value := resolved[secret.Name]; value != "" {
			merged[secret.Env] = value
		}
	}
	for key, value := range nodeEnv {
		merged[key] = value
	}
	return merged
}

// storeArtifact writes one immutable artifact: payload to the content
// store (deduplicated), metadata to state.db with a retention expiry.
func (s *Service) storeArtifact(runID, nodeID string, attemptID int64, name, kind string, payload []byte, retentionDays int) {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()

	hash := Hash(payload)
	if s.blobs != nil {
		stored, err := s.blobs.Put(payload)
		if err != nil {
			return
		}
		hash = stored
	}
	_ = s.store.InsertWorkflowArtifact(state.WorkflowArtifact{
		ID:          newID("wfa"),
		RunID:       runID,
		NodeID:      nodeID,
		AttemptID:   attemptID,
		Name:        name,
		Kind:        kind,
		ContentHash: hash,
		SizeBytes:   int64(len(payload)),
		CreatedAt:   s.now().UnixMilli(),
		ExpiresAt:   retentionExpiry(s.now(), retentionDays),
	})
}

// ListArtifacts returns historical/public artifact metadata. Internal map-item
// payloads are deliberately omitted so they cannot look like node outputs.
func (s *Service) ListArtifacts(_ context.Context, runID string) ([]Artifact, error) {
	rows, err := s.store.ListWorkflowArtifacts(runID)
	if err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, len(rows))
	for _, row := range rows {
		if row.AttemptID == 0 && row.Name == "item" {
			continue
		}
		out = append(out, artifactFromState(row))
	}
	return out, nil
}

// upstreamNodes returns the set of nodes reachable by following
// dependency edges backwards from nodeID (its declared ancestors).
func upstreamNodes(dependencies []Dependency, nodeID string) map[string]bool {
	parents := map[string][]string{}
	for _, dep := range dependencies {
		parents[dep.To] = append(parents[dep.To], dep.From)
	}
	seen := map[string]bool{}
	stack := append([]string{}, parents[nodeID]...)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		stack = append(stack, parents[current]...)
	}
	return seen
}

// DownloadArtifact returns the metadata and payload bytes for an
// artifact. Returns ErrPayloadMissing (wrapped) when retention cleanup
// has dropped the payload (metadata is preserved).
func (s *Service) DownloadArtifact(_ context.Context, runID, id string) (Artifact, []byte, error) {
	return s.downloadArtifact(id, runID, false)
}

func (s *Service) downloadArtifact(id, runID string, internal bool) (Artifact, []byte, error) {
	row, err := s.store.GetWorkflowArtifact(id)
	if err != nil {
		return Artifact{}, nil, err
	}
	if (!internal && row.AttemptID == 0 && row.Name == "item") || (runID != "" && row.RunID != runID) {
		return Artifact{}, nil, fmt.Errorf("artifact not found")
	}
	artifact := artifactFromState(*row)
	if row.PayloadDeleted {
		return artifact, nil, fmt.Errorf("%s: %w", id, ErrPayloadMissing)
	}
	if s.blobs == nil {
		return artifact, nil, fmt.Errorf("%s: %w", id, ErrPayloadMissing)
	}
	payload, err := s.blobs.Get(row.ContentHash)
	if err != nil {
		return artifact, nil, err
	}
	return artifact, payload, nil
}

// CleanupExpiredPayloads removes content-store payloads whose every
// referencing artifact has passed its retention window, then marks
// those metadata rows payload_deleted. Metadata is always preserved so
// old run outcomes remain auditable. Returns the number of payloads
// removed.
func (s *Service) CleanupExpiredPayloads(_ context.Context) (int, error) {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()

	hashes, err := s.store.ExpiredWorkflowArtifactHashes(s.now().UnixMilli())
	if err != nil {
		return 0, err
	}
	var removed int
	var errs []error
	for _, hash := range hashes {
		if s.blobs != nil {
			if err := s.blobs.Remove(hash); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := s.store.MarkWorkflowArtifactPayloadDeleted(hash); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
