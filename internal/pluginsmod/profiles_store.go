package pluginsmod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
)

var (
	ErrPermissionProfileNotFound  = errors.New("permission profile not found")
	ErrPermissionProfileNameTaken = errors.New("permission profile name already exists")
)

// PermissionProfile is a user-managed named permission ruleset: a pinned
// list of platforms.PermissionRule entries stored as JSON, resolved by name
// (ResolvePermissionProfile) and passed verbatim to
// sessionsvc.CreateConfigured when a session starts with the profile.
//
// Nothing is seeded; the documented example is a "read-only" profile that
// denies everything that can change state:
//
//	{"name":"read-only","rules":[
//	  {"permission":"bash","pattern":"*","action":"deny"},
//	  {"permission":"edit","pattern":"*","action":"deny"},
//	  {"permission":"write","pattern":"*","action":"deny"}]}
type PermissionProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RulesJSON string `json:"rulesJSON"`
}

const permissionProfileColumns = `id, name, rules_json`

type permissionProfileScanner interface{ Scan(...any) error }

func scanPermissionProfile(row permissionProfileScanner) (PermissionProfile, error) {
	var profile PermissionProfile
	err := row.Scan(&profile.ID, &profile.Name, &profile.RulesJSON)
	return profile, err
}

func (d *Store) CreatePermissionProfile(ctx context.Context, profile PermissionProfile) error {
	_, err := d.db.ExecContext(ctx, `INSERT INTO plugin_permission_profiles (`+permissionProfileColumns+`) VALUES (?, ?, ?)`,
		profile.ID, profile.Name, profile.RulesJSON)
	if err != nil {
		return normalizePermissionProfileWriteError(fmt.Errorf("creating permission profile: %w", err))
	}
	return nil
}

func (d *Store) GetPermissionProfile(ctx context.Context, id string) (PermissionProfile, error) {
	profile, err := scanPermissionProfile(d.db.QueryRowContext(ctx, `SELECT `+permissionProfileColumns+` FROM plugin_permission_profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PermissionProfile{}, ErrPermissionProfileNotFound
	}
	if err != nil {
		return PermissionProfile{}, fmt.Errorf("getting permission profile: %w", err)
	}
	return profile, nil
}

func (d *Store) ListPermissionProfiles(ctx context.Context) ([]PermissionProfile, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT `+permissionProfileColumns+` FROM plugin_permission_profiles ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("listing permission profiles: %w", err)
	}
	defer rows.Close()
	var profiles []PermissionProfile
	for rows.Next() {
		profile, err := scanPermissionProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning permission profile: %w", err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (d *Store) UpdatePermissionProfile(ctx context.Context, profile PermissionProfile) error {
	result, err := d.db.ExecContext(ctx, `UPDATE plugin_permission_profiles SET name = ?, rules_json = ? WHERE id = ?`,
		profile.Name, profile.RulesJSON, profile.ID)
	if err != nil {
		return normalizePermissionProfileWriteError(fmt.Errorf("updating permission profile: %w", err))
	}
	return requirePermissionProfileChange(result)
}

func (d *Store) DeletePermissionProfile(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, `DELETE FROM plugin_permission_profiles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting permission profile: %w", err)
	}
	return requirePermissionProfileChange(result)
}

// ResolvePermissionProfile returns the rules pinned under the (case-insensitive)
// profile name. A missing or deleted profile is ErrPermissionProfileNotFound,
// never a silently empty rule set.
func (d *Store) ResolvePermissionProfile(ctx context.Context, name string) ([]platforms.PermissionRule, error) {
	var rulesJSON string
	err := d.db.QueryRowContext(ctx, `SELECT rules_json FROM plugin_permission_profiles WHERE name = ?`, name).Scan(&rulesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPermissionProfileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolving permission profile: %w", err)
	}
	var rules []platforms.PermissionRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		return nil, fmt.Errorf("decoding permission profile %q: %w", name, err)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("permission profile %q stores no rules", name)
	}
	return rules, nil
}

func requirePermissionProfileChange(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrPermissionProfileNotFound
	}
	return nil
}

func normalizePermissionProfileWriteError(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed: plugin_permission_profiles.name") {
		return fmt.Errorf("%w: %w", ErrPermissionProfileNameTaken, err)
	}
	return err
}
