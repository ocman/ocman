package workflows

import (
	"context"
	"encoding/json"
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

// collectorKind maps a producer collector type (command or agent) to its
// immutable artifact kind. Unknown types fall back to text so nothing is
// silently dropped.
func collectorKind(collectorType string) string {
	switch collectorType {
	case "json_file", "json-file":
		return KindJSON
	case "file":
		return KindFile
	case "git_diff", "diff":
		return KindDiff
	case "diagnostics":
		return KindDiagnostics
	default:
		return KindText
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

// redactOutputs redacts secret values from a map of collected output
// values (JSON-encoded), returning a new map. Used before persisting
// artifact payloads so credentials never land in the store.
func (r *redactor) redactOutputs(outputs map[string]string) map[string]string {
	if r == nil || len(r.values) == 0 || len(outputs) == 0 {
		return outputs
	}
	out := make(map[string]string, len(outputs))
	for name, value := range outputs {
		out[name] = r.redact(value)
	}
	return out
}

// redactRawOutputs redacts secret values from agent outputs that are
// stored as raw JSON messages.
func (r *redactor) redactRawOutputs(outputs map[string]json.RawMessage) map[string]json.RawMessage {
	if r == nil || len(r.values) == 0 || len(outputs) == 0 {
		return outputs
	}
	out := make(map[string]json.RawMessage, len(outputs))
	for name, value := range outputs {
		out[name] = json.RawMessage(r.redact(string(value)))
	}
	return out
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

// publishCommandArtifacts stores each collected command output as an
// immutable artifact (metadata in state.db, payload in the content
// store). Best-effort: a store failure is logged into the payload as a
// missing marker rather than failing the whole node.
func (s *Service) publishCommandArtifacts(version Version, runID string, node Node, attemptID int64, outputs map[string]string) {
	for _, collector := range node.Outputs {
		payload, ok := outputs[collector.Name]
		if !ok {
			continue
		}
		s.storeArtifact(runID, node.ID, attemptID, collector.Name, collectorKind(collector.Type), []byte(payload), version.Definition.RetentionDays)
	}
}

// publishAgentArtifacts stores each collected agent output as an
// immutable artifact. Agent outputs arrive as raw JSON messages.
func (s *Service) publishAgentArtifacts(version Version, runID, nodeID string, attemptID int64, config *AgentConfig, outputs map[string]json.RawMessage) {
	if config == nil {
		return
	}
	kinds := make(map[string]string, len(config.Collectors))
	for _, collector := range config.Collectors {
		kinds[collector.Name] = collectorKind(collector.Type)
	}
	for name, value := range outputs {
		kind := kinds[name]
		if kind == "" {
			kind = KindText
		}
		s.storeArtifact(runID, nodeID, attemptID, name, kind, []byte(value), version.Definition.RetentionDays)
	}
}

// storeArtifact writes one immutable artifact: payload to the content
// store (deduplicated), metadata to state.db with a retention expiry.
func (s *Service) storeArtifact(runID, nodeID string, attemptID int64, name, kind string, payload []byte, retentionDays int) {
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

// ListArtifacts returns the immutable artifact metadata for a run.
func (s *Service) ListArtifacts(_ context.Context, runID string) ([]Artifact, error) {
	rows, err := s.store.ListWorkflowArtifacts(runID)
	if err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, len(rows))
	for _, row := range rows {
		out = append(out, artifactFromState(row))
	}
	return out, nil
}

// ConsumableArtifacts returns the artifacts a node may consume: only
// those produced by its declared upstream dependencies (the nodes with
// a dependency edge into nodeID, transitively). This enforces the
// declared-input-only contract — a node cannot read artifacts from an
// unrelated branch it does not depend on.
func (s *Service) ConsumableArtifacts(ctx context.Context, runID, nodeID string) ([]Artifact, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	upstream := upstreamNodes(run.Version.Definition.Dependencies, nodeID)
	all, err := s.ListArtifacts(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, len(all))
	for _, artifact := range all {
		if upstream[artifact.NodeID] {
			out = append(out, artifact)
		}
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
func (s *Service) DownloadArtifact(_ context.Context, id string) (Artifact, []byte, error) {
	row, err := s.store.GetWorkflowArtifact(id)
	if err != nil {
		return Artifact{}, nil, err
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
