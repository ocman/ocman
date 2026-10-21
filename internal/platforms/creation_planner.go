package platforms

// SessionCreationPlanner is optional for integrations accepting client-chosen
// native IDs. The ID must be stable for a canonical identity, unique across
// identities, and honored atomically by native creation. Empty means unsupported.
// The owner persists this binding before calling the integration, so a lost
// response can be recovered by exact ID without guessing or creating duplicates.
type SessionCreationPlanner interface {
	PlannedSessionID(canonicalID string) string
}
