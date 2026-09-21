package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

var (
	ErrPluginState    = errors.New("plugin state operation failed")
	ErrPluginNotFound = errors.New("plugin registration not found")
	ErrPluginInvalid  = errors.New("invalid plugin state")
	pluginKey         = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$`)
)

func migrateToV91(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_registration (
		id TEXT PRIMARY KEY,
		description_json TEXT NOT NULL,
		executable_path TEXT NOT NULL,
		checksum TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
		removed INTEGER NOT NULL DEFAULT 0 CHECK(removed IN (0,1)),
		instances_json TEXT NOT NULL DEFAULT '[]',
		grants_json TEXT NOT NULL DEFAULT '[]',
		config_json TEXT NOT NULL DEFAULT '{}',
		secret_file TEXT NOT NULL DEFAULT '',
		working_config_json TEXT,
		working_secret_file TEXT,
		health_json TEXT NOT NULL DEFAULT '{}'
	)`)
	return err
}

// PluginInstance identifies an independently addressable capability instance.
// These are host-local identities; remote routing supplies the owner separately.
type PluginInstance struct {
	ID         string             `json:"id"`
	Capability plugins.Capability `json:"capability"`
	Scope      plugins.Scope      `json:"scope"`
}

type PluginHealth struct {
	Status       string `json:"status"`
	RestartCount int    `json:"restartCount"`
	LastError    string `json:"lastError,omitempty"`
}

// PluginConfiguration contains public values and secret presence only.
// Secret values never pass through a registration, SQL, or its telemetry.
type PluginConfiguration struct {
	Values  map[string]json.RawMessage `json:"values"`
	Secrets map[string]bool            `json:"secrets"`
}

type PluginRegistration struct {
	Approval                 string               `json:"approval"`
	Description              plugins.Description  `json:"description"`
	ExecutablePath           string               `json:"executablePath"`
	Checksum                 string               `json:"checksum"`
	Enabled                  bool                 `json:"enabled"`
	Removed                  bool                 `json:"removed"`
	Instances                []PluginInstance     `json:"instances"`
	Grants                   []string             `json:"grants"`
	Configuration            PluginConfiguration  `json:"configuration"`
	LastWorkingConfiguration *PluginConfiguration `json:"lastWorkingConfiguration,omitempty"`
	Health                   PluginHealth         `json:"health"`
}

// DiscoverPlugin never enables a plugin. Changed code or declarations revoke
// approval, while rediscovery preserves configuration and private data.
func (d *DB) DiscoverPlugin(ctx context.Context, description plugins.Description, path, checksum string, instances []PluginInstance) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	decoded, err := hex.DecodeString(checksum)
	if description.Validate() != nil || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || err != nil || len(decoded) != 32 || checksum != strings.ToLower(checksum) {
		return ErrPluginInvalid
	}
	seen := map[string]bool{}
	for _, instance := range instances {
		if !pluginKey.MatchString(instance.ID) || seen[instance.ID] || instance.Scope != description.Scope {
			return ErrPluginInvalid
		}
		seen[instance.ID] = true
		found := false
		for _, capability := range description.Capabilities {
			if capability == instance.Capability {
				found = true
			}
		}
		if !found {
			return ErrPluginInvalid
		}
	}
	desc, _ := json.Marshal(description)
	caps, _ := json.Marshal(instances)
	_, err = d.db.ExecContext(ctx, `INSERT INTO plugin_registration(id,description_json,executable_path,checksum,instances_json)
		VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		enabled=CASE WHEN description_json=excluded.description_json AND executable_path=excluded.executable_path AND checksum=excluded.checksum AND instances_json=excluded.instances_json AND removed=0 THEN enabled ELSE 0 END,
		grants_json=CASE WHEN description_json=excluded.description_json AND executable_path=excluded.executable_path AND checksum=excluded.checksum AND instances_json=excluded.instances_json AND removed=0 THEN grants_json ELSE '[]' END,
		description_json=excluded.description_json, executable_path=excluded.executable_path,
		checksum=excluded.checksum, instances_json=excluded.instances_json, removed=0`, description.ID, string(desc), path, checksum, string(caps))
	return pluginStateError(err)
}

func pluginStateError(err error) error {
	if err != nil {
		return ErrPluginState
	}
	return nil
}

func (d *DB) pluginUpdate(ctx context.Context, query string, args ...any) error {
	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrPluginState
	}
	if n == 0 {
		return ErrPluginNotFound
	}
	return nil
}

// SetPluginEnabled atomically records explicit enablement and approved grants.
// Disabling preserves grants/configuration; SetPluginGrants handles revocation.
func (d *DB) SetPluginEnabled(ctx context.Context, id string, enabled bool, grants []string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	if !enabled {
		return d.pluginUpdate(ctx, `UPDATE plugin_registration SET enabled=0 WHERE id=?`, id)
	}
	if !validPluginGrants(grants) {
		return ErrPluginInvalid
	}
	data, _ := json.Marshal(grants)
	return d.pluginUpdate(ctx, `UPDATE plugin_registration SET enabled=1,grants_json=? WHERE id=? AND removed=0`, string(data), id)
}

func validPluginGrants(grants []string) bool {
	seen := map[string]bool{}
	for _, grant := range grants {
		if !pluginKey.MatchString(grant) || seen[grant] {
			return false
		}
		seen[grant] = true
	}
	return true
}

func (d *DB) SetPluginGrants(ctx context.Context, id string, grants []string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	if !validPluginGrants(grants) {
		return ErrPluginInvalid
	}
	data, _ := json.Marshal(grants)
	return d.pluginUpdate(ctx, `UPDATE plugin_registration SET grants_json=? WHERE id=?`, string(data), id)
}

// RemovePlugin is a tombstone, not a destructive deletion.
func (d *DB) RemovePlugin(ctx context.Context, id string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	return d.pluginUpdate(ctx, `UPDATE plugin_registration SET enabled=0,removed=1 WHERE id=?`, id)
}

// WithPluginAuthorization serializes action admission with disable, rediscovery,
// deletion and grant revocation. The callback must not call state methods taking
// pluginMu or wait for plugin results. Only public declarations cross this seam.
func (d *DB) WithPluginAuthorization(ctx context.Context, id string, use func(plugins.Description, []string) error) error {
	return d.WithPluginConversation(ctx, id, func(desc plugins.Description, approved []string, _ string) error {
		return use(desc, approved)
	})
}

// WithPluginConversation is WithPluginAuthorization plus the plugin's one
// approved conversation project, read from the same row under the same lock so
// a reconfiguration cannot land between the grant check and the project check.
// An unset or non-string setting yields an empty project, which fails closed.
func (d *DB) WithPluginConversation(ctx context.Context, id string, use func(plugins.Description, []string, string) error) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	var description, grants, config string
	var enabled, removed bool
	err := d.db.QueryRowContext(ctx, `SELECT description_json, grants_json, config_json, enabled, removed FROM plugin_registration WHERE id=?`, id).
		Scan(&description, &grants, &config, &enabled, &removed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (!enabled || removed)) {
		return plugins.ErrUnavailable
	}
	if err != nil {
		return ErrPluginState
	}
	var desc plugins.Description
	var approved []string
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(description), &desc) != nil || json.Unmarshal([]byte(grants), &approved) != nil || json.Unmarshal([]byte(config), &values) != nil {
		return ErrPluginState
	}
	var project string
	_ = json.Unmarshal(values[plugins.ConversationProjectSetting], &project)
	return use(desc, approved, project)
}

func (d *DB) GetPlugin(ctx context.Context, id string) (PluginRegistration, error) {
	var p PluginRegistration
	var desc, instances, grants, config, secret, health string
	var working, workingSecret sql.NullString
	err := d.db.QueryRowContext(ctx, `SELECT description_json,executable_path,checksum,enabled,removed,instances_json,grants_json,config_json,secret_file,working_config_json,working_secret_file,health_json FROM plugin_registration WHERE id=?`, id).
		Scan(&desc, &p.ExecutablePath, &p.Checksum, &p.Enabled, &p.Removed, &instances, &grants, &config, &secret, &working, &workingSecret, &health)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrPluginNotFound
	}
	if err != nil {
		return p, ErrPluginState
	}
	for _, field := range []struct {
		data   string
		target any
	}{{desc, &p.Description}, {instances, &p.Instances}, {grants, &p.Grants}, {health, &p.Health}} {
		if json.Unmarshal([]byte(field.data), field.target) != nil {
			return PluginRegistration{}, ErrPluginState
		}
	}
	approved, _ := json.Marshal([]any{p.Description, p.ExecutablePath, p.Checksum, p.Instances})
	fingerprint := sha256.Sum256(approved)
	p.Approval = hex.EncodeToString(fingerprint[:])
	p.Configuration, err = d.pluginConfiguration(id, config, secret)
	if err != nil {
		return PluginRegistration{}, err
	}
	if working.Valid {
		value, err := d.pluginConfiguration(id, working.String, workingSecret.String)
		if err != nil {
			return PluginRegistration{}, err
		}
		p.LastWorkingConfiguration = &value
	}
	p.Health.LastError = d.RedactPluginDiagnostics(id, p.Health.LastError)
	return p, nil
}

// ListPlugins includes tombstones so retained configuration remains manageable.
func (d *DB) ListPlugins(ctx context.Context) ([]PluginRegistration, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id FROM plugin_registration ORDER BY id`)
	if err != nil {
		return nil, ErrPluginState
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, ErrPluginState
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close() // Release the single connection before GetPlugin.
	if err != nil {
		return nil, ErrPluginState
	}
	out := make([]PluginRegistration, 0, len(ids))
	for _, id := range ids {
		p, err := d.GetPlugin(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (d *DB) SetPluginHealth(ctx context.Context, id string, health PluginHealth) error {
	return d.setPluginHealth(ctx, id, health, "")
}

// SetPluginProcessHealth cannot overwrite disabled/conflicted discovery state
// when a late readiness or exit notification races an enablement change.
func (d *DB) SetPluginProcessHealth(ctx context.Context, id string, health PluginHealth) error {
	return d.setPluginHealth(ctx, id, health, " AND enabled=1 AND removed=0")
}

func (d *DB) setPluginHealth(ctx context.Context, id string, health PluginHealth, condition string) error {
	switch health.Status {
	case "unknown", "disabled", "starting", "ready", "unhealthy", "conflict", "stopped":
	default:
		return ErrPluginInvalid
	}
	if health.RestartCount < 0 {
		return ErrPluginInvalid
	}
	health.LastError = d.RedactPluginDiagnostics(id, health.LastError)
	data, _ := json.Marshal(health)
	return d.pluginUpdate(ctx, `UPDATE plugin_registration SET health_json=? WHERE id=?`+condition, string(data), id)
}

func (d *DB) MarkPluginConfigurationWorking(ctx context.Context, id string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	return d.pluginUpdate(ctx, `UPDATE plugin_registration SET working_config_json=config_json,working_secret_file=secret_file WHERE id=?`, id)
}

func (d *DB) RollbackPluginConfiguration(ctx context.Context, id string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	return d.pluginUpdate(ctx, `UPDATE plugin_registration
		SET config_json=working_config_json,secret_file=working_secret_file,
		    health_json='{"status":"starting","restartCount":0}'
		WHERE id=? AND working_config_json IS NOT NULL`, id)
}

// RecoverPluginConfigurations restores enabled plugins whose candidate never
// reached its working checkpoint. Disabled edits remain available for enablement.
func (d *DB) RecoverPluginConfigurations(ctx context.Context) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	_, err := d.db.ExecContext(ctx, `UPDATE plugin_registration
		SET config_json=working_config_json,secret_file=working_secret_file,
		    health_json='{"status":"starting","restartCount":0}'
		WHERE enabled=1 AND working_config_json IS NOT NULL
		AND (config_json!=working_config_json OR secret_file!=working_secret_file)`)
	return pluginStateError(err)
}
