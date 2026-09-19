package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Plugin-owned data and host-owned secret snapshots are siblings. A plugin's
// working directory must never contain the host's secret store.
func (d *DB) pluginDirectory(id, kind string) (*os.Root, error) {
	if d.dataDir == "" || id == "" {
		return nil, ErrPluginState
	}
	root, err := os.OpenRoot(d.dataDir)
	if err != nil {
		return nil, ErrPluginState
	}
	sum := sha256.Sum256([]byte(id))
	for _, name := range []string{kind, hex.EncodeToString(sum[:])} {
		err := root.Mkdir(name, 0o700)
		if err != nil && !errors.Is(err, os.ErrExist) {
			_ = root.Close()
			return nil, ErrPluginState
		}
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			_ = root.Close()
			return nil, ErrPluginState
		}
		if err := root.Chmod(name, 0o700); err != nil {
			_ = root.Close()
			return nil, ErrPluginState
		}
		next, err := root.OpenRoot(name)
		_ = root.Close()
		if err != nil {
			return nil, ErrPluginState
		}
		root = next
	}
	return root, nil
}

func (d *DB) PluginDataDir(ctx context.Context, id string) (string, error) {
	var exists bool
	if err := d.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_registration WHERE id=?)`, id).Scan(&exists); err != nil {
		return "", ErrPluginState
	}
	if !exists {
		return "", ErrPluginNotFound
	}
	root, err := d.pluginDirectory(id, "plugin-data")
	if err != nil {
		return "", err
	}
	defer root.Close()
	path, err := filepath.Abs(root.Name())
	if err != nil {
		return "", ErrPluginState
	}
	return path, nil
}

func (d *DB) readPluginSecrets(id, name string) (map[string]string, error) {
	secrets := map[string]string{}
	if name == "" {
		return secrets, nil
	}
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".json") {
		return nil, ErrPluginState
	}
	root, err := d.pluginDirectory(id, "plugin-secrets")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, ErrPluginState
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, ErrPluginState
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &secrets) != nil || secrets == nil {
		return nil, ErrPluginState
	}
	return secrets, nil
}

func (d *DB) pluginConfiguration(id, config, file string) (PluginConfiguration, error) {
	p := PluginConfiguration{Secrets: map[string]bool{}}
	if json.Unmarshal([]byte(config), &p.Values) != nil {
		return p, ErrPluginState
	}
	secrets, err := d.readPluginSecrets(id, file)
	if err != nil {
		return PluginConfiguration{}, err
	}
	for key, value := range secrets {
		p.Secrets[key] = value != ""
	}
	return p, nil
}

// SetPluginConfiguration replaces public values and patches secrets. Omitted
// secret keys are retained; an empty value explicitly clears a secret. Callers
// must split fields using the plugin's settings schema before calling this.
// Immutable secret snapshots are synced before their SQL pointer is committed.
// Failed SQL writes leave the prior configuration active.
func (d *DB) SetPluginConfiguration(ctx context.Context, id string, values map[string]json.RawMessage, updates map[string]string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	for key, value := range values {
		if !pluginKey.MatchString(key) || !json.Valid(value) {
			return ErrPluginInvalid
		}
	}
	for key := range updates {
		if !pluginKey.MatchString(key) {
			return ErrPluginInvalid
		}
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrPluginState
	}
	defer func() { _ = tx.Rollback() }()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT secret_file FROM plugin_registration WHERE id=?`, id).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPluginNotFound
	}
	if err != nil {
		return ErrPluginState
	}
	secrets, err := d.readPluginSecrets(id, previous)
	if err != nil {
		return err
	}
	for key, value := range updates {
		if value == "" {
			delete(secrets, key)
		} else {
			secrets[key] = value
		}
	}
	for key := range secrets {
		if _, ok := values[key]; ok {
			return ErrPluginInvalid
		}
	}
	public, err := json.Marshal(values)
	if err != nil || len(public) > 1<<20 {
		return ErrPluginInvalid
	}
	private, err := json.Marshal(secrets)
	if err != nil || len(private) > 1<<20 {
		return ErrPluginInvalid
	}
	root, err := d.pluginDirectory(id, "plugin-secrets")
	if err != nil {
		return err
	}
	defer root.Close()
	name := rand.Text() + ".json"
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrPluginState
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(name)
		}
	}()
	_, writeErr := f.Write(private)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrPluginState
	}
	dir, err := root.Open(".")
	if err != nil {
		return ErrPluginState
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return ErrPluginState
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_registration SET config_json=?,secret_file=? WHERE id=?`, string(public), name, id); err != nil {
		return ErrPluginState
	}
	if err := tx.Commit(); err != nil {
		return ErrPluginState
	}
	committed = true
	return nil
}

// WithPluginSecrets is for local process launch only, never management reads.
// Callback errors are replaced, not wrapped: arbitrary plugin errors may contain
// credentials in encodings no string-replacement redactor can recognize.
func (d *DB) WithPluginSecrets(ctx context.Context, id string, use func(map[string]string) error) error {
	var name string
	err := d.db.QueryRowContext(ctx, `SELECT secret_file FROM plugin_registration WHERE id=?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPluginNotFound
	}
	if err != nil {
		return ErrPluginState
	}
	secrets, err := d.readPluginSecrets(id, name)
	if err != nil {
		return err
	}
	return pluginStateError(use(secrets))
}

// DeletePluginPermanently explicitly forgets a disabled registration and all its
// configuration, secret history and private data. File cleanup can be retried
// after an I/O failure even when the registration has already been deleted.
func (d *DB) DeletePluginPermanently(ctx context.Context, id string) error {
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	var enabled bool
	err := d.db.QueryRowContext(ctx, `SELECT enabled FROM plugin_registration WHERE id=?`, id).Scan(&enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ErrPluginState
	}
	if enabled || id == "" {
		return ErrPluginInvalid
	}
	if d.dataDir == "" {
		return ErrPluginState
	}
	root, err := os.OpenRoot(d.dataDir)
	if err != nil {
		return ErrPluginState
	}
	defer root.Close()
	if _, err := d.db.ExecContext(ctx, `DELETE FROM plugin_registration WHERE id=?`, id); err != nil {
		return ErrPluginState
	}
	sum := sha256.Sum256([]byte(id))
	for _, kind := range []string{"plugin-data", "plugin-secrets"} {
		if err := root.RemoveAll(filepath.Join(kind, hex.EncodeToString(sum[:]))); err != nil {
			return ErrPluginState
		}
	}
	return nil
}

// RedactPluginDiagnostics filters current AND historical secrets, including
// failed configuration candidates. Unreadable secret history fails closed.
// This is best-effort for text diagnostics, not a sandbox for trusted code.
// ponytail: scan retained snapshots; cache a redactor if rotation volume warrants it.
func (d *DB) RedactPluginDiagnostics(id, text string) string {
	if text == "" {
		return ""
	}
	d.pluginMu.Lock()
	defer d.pluginMu.Unlock()
	root, err := d.pluginDirectory(id, "plugin-secrets")
	if err != nil {
		return "[diagnostics unavailable]"
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return "[diagnostics unavailable]"
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return "[diagnostics unavailable]"
	}
	var values []string
	for _, entry := range entries {
		secrets, err := d.readPluginSecrets(id, entry.Name())
		if err != nil {
			return "[diagnostics unavailable]"
		}
		for _, value := range secrets {
			if value == "" {
				continue
			}
			encoded, _ := json.Marshal(value)
			values = append(values, value, string(encoded[1:len(encoded)-1]), url.QueryEscape(value), url.PathEscape(value))
		}
	}
	// Longest first avoids leaking the suffix of overlapping credentials.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[REDACTED]")
		// A live stderr read may end partway through a credential.
		for n := min(len(value)-1, len(text)); n > 0; n-- {
			if strings.HasSuffix(text, value[:n]) {
				text = strings.TrimSuffix(text, value[:n]) + "[REDACTED]"
				break
			}
		}
	}
	return text
}
