package platformconnections

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Seed is a snapshot supplied by the owner before startup creates native state.
// ExistingInstallation must not be inferred from the migrated schema or an
// empty platform setting. An existing installation can have no enabled tools.
// Connections describes legacy configurations, including disabled ones with
// saved references, not transient CLI overrides.
type Seed struct {
	ExistingInstallation bool
	Connections          []Input
	DefaultKind          string
}

// Initialize persists the collection once, atomically with its legacy mappings.
// A fresh installation starts empty with onboarding incomplete. An existing
// installation skips onboarding, even if its explicit collection is empty.
// Repeated calls never reseed an empty collection or resurrect removed entries.
// The bool reports whether this call initialized it. Startup integration remains
// deferred until connection-scoped execution can consume the same identities.
func (s *Store) Initialize(ctx context.Context, seed Seed) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO platform_connection_collection(id,onboarding_complete)
		VALUES(1,?) ON CONFLICT(id) DO NOTHING`, seed.ExistingInstallation)
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if (!seed.ExistingInstallation && (len(seed.Connections) != 0 || seed.DefaultKind != "")) || len(seed.Connections) > maxConnections {
		return false, fmt.Errorf("%w: invalid legacy seed", ErrInvalid)
	}
	var defaultID string
	seen := map[string]bool{}
	for _, input := range seed.Connections {
		// Legacy configurations may not have an explicit directory yet. Do not
		// resolve paths or touch native state while recording the old identity.
		input, err = validate(input, true)
		if err != nil {
			return false, err
		}
		if seen[input.Kind] || input.Configuration != "existing" {
			return false, fmt.Errorf("%w: legacy seed must contain one existing connection per kind", ErrInvalid)
		}
		seen[input.Kind] = true
		id := "cnx_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled,legacy_kind)
			VALUES(?,?,?,?,?,?,?)`, id, input.Kind, input.Name, input.Configuration, input.Directory, input.Enabled, input.Kind); err != nil {
			return false, err
		}
		if seed.DefaultKind == input.Kind {
			defaultID = id
		}
	}
	if seed.DefaultKind != "" && defaultID == "" {
		return false, fmt.Errorf("%w: legacy default is not in the collection", ErrInvalid)
	}
	if defaultID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE platform_connection_collection SET default_id=? WHERE id=1`, defaultID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
