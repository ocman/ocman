package state

import (
	"fmt"
	"time"
)

// ModelFavorite identifies one favorited model. `Provider` may be
// empty for platforms that don't have a provider concept.
type ModelFavorite struct {
	Platform string
	Provider string
	Model    string
}

// AddModelFavorite marks a (platform, provider, model) triple as a
// favorite. Idempotent: repeated calls are no-ops.
func (d *DB) AddModelFavorite(platform, provider, model string) error {
	_, err := d.db.Exec(`
		INSERT INTO model_favorite (platform, provider_id, model_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, provider_id, model_id) DO NOTHING
	`, platform, provider, model, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("adding model favorite: %w", err)
	}
	return nil
}

// RemoveModelFavorite unfavorites a (platform, provider, model)
// triple. No error if the row doesn't exist.
func (d *DB) RemoveModelFavorite(platform, provider, model string) error {
	_, err := d.db.Exec(
		`DELETE FROM model_favorite WHERE platform = ? AND provider_id = ? AND model_id = ?`,
		platform, provider, model,
	)
	if err != nil {
		return fmt.Errorf("removing model favorite: %w", err)
	}
	return nil
}

// ModelFavorites returns every favorited model for the given platform,
// ordered by creation time ascending (oldest favorite first).
func (d *DB) ModelFavorites(platform string) ([]ModelFavorite, error) {
	rows, err := d.db.Query(
		`SELECT platform, provider_id, model_id FROM model_favorite
		 WHERE platform = ?
		 ORDER BY created_at ASC`,
		platform,
	)
	if err != nil {
		return nil, fmt.Errorf("listing model favorites: %w", err)
	}
	defer rows.Close()

	var out []ModelFavorite
	for rows.Next() {
		var f ModelFavorite
		if err := rows.Scan(&f.Platform, &f.Provider, &f.Model); err != nil {
			return nil, fmt.Errorf("scanning model favorite: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading model favorites: %w", err)
	}
	return out, nil
}
