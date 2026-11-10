package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/ocman/ocman/internal/configfile"
)

type fileConfig struct {
	file     *configfile.File
	resolver configfile.Resolver
}

// EnableFileConfig migrates once, then makes the file authoritative for the
// allowlisted user settings. Old database rows are retained but never consulted
// for a missing file value. Call before serving, after opening the database.
func (d *DB) EnableFileConfig(ctx context.Context, path string) error {
	file := &configfile.File{Path: path}
	doc, err := file.Read()
	if errors.Is(err, os.ErrNotExist) {
		var migrated string
		queryErr := d.reader().QueryRowContext(ctx, `SELECT value FROM setting WHERE key = 'config.file_migrated'`).Scan(&migrated)
		if queryErr == nil {
			return errors.New("configuration is missing after migration; restore the file instead of importing stale database settings")
		}
		if !errors.Is(queryErr, sql.ErrNoRows) {
			return queryErr
		}
		doc, err = d.migrateFileConfig(ctx)
		if err == nil {
			err = file.Create(doc)
			if errors.Is(err, os.ErrExist) {
				doc, err = file.Read()
			}
		}
	}
	if err != nil {
		return err
	}
	if err := validateFileSettings(doc); err != nil {
		return err
	}
	fc := &fileConfig{file: file, resolver: configfile.NewResolver(d.loadConfigSecret)}
	// Required references fail before listeners start serving, never falling back
	// to old database credentials when a provider is unavailable.
	for _, key := range doc.Keys("settings") {
		if !isFileSecret(key) && key != "judge_endpoint" {
			continue
		}
		if _, _, err := fc.get(ctx, key); err != nil {
			return fmt.Errorf("resolving configured setting %s: %w", key, err)
		}
	}
	if err := d.setSetting(ctx, "config.file_migrated", path); err != nil {
		return err
	}
	d.fileConfig.Store(fc)
	return nil
}

func validateFileSettings(doc *configfile.Document) error {
	for _, key := range doc.Keys("settings") {
		if !isFileSetting(key) {
			return errors.New("unknown configuration setting")
		}
		value, _, err := doc.Get("settings", key)
		if err != nil {
			return err
		}
		if isFileSecret(key) && value != "" {
			if _, err := configfile.ParseSecret(value); err != nil {
				return err
			}
		}
		if key == managedJudgeDelayKey {
			if delay, err := strconv.ParseInt(value, 10, 64); err != nil || delay < 0 {
				return errors.New("judge_delay must be a nonnegative integer")
			}
		}
		if key == "judge_endpoint" {
			fields, err := endpointFields(value)
			if err != nil {
				return err
			}
			if secret, _ := fields["apiKey"].(string); secret != "" {
				if _, err := configfile.ParseSecret(secret); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (f *fileConfig) get(ctx context.Context, key string) (string, bool, error) {
	doc, err := f.file.Read()
	if err != nil {
		return "", false, err
	}
	if err := validateFileSettings(doc); err != nil {
		return "", false, err
	}
	value, ok, err := doc.Get("settings", key)
	if err != nil || !ok || value == "" {
		return value, ok, err
	}
	if key == settingWorktreeInheritPermissions {
		// The legacy interface uses "0"/"1"; YAML users may use booleans.
		switch value {
		case "false":
			value = "0"
		case "true":
			value = "1"
		}
	}
	if isFileSecret(key) {
		value, err = f.resolver.Resolve(ctx, value)
	}
	if key == "judge_endpoint" {
		var fields map[string]any
		fields, err = endpointFields(value)
		if err == nil {
			if ref, _ := fields["apiKey"].(string); ref != "" {
				fields["apiKey"], err = f.resolver.Resolve(ctx, ref)
			}
			if err == nil {
				var b []byte
				b, err = json.Marshal(fields)
				value = string(b)
			}
		}
	}
	return value, ok, err
}

func endpointFields(value string) (map[string]any, error) {
	fields := map[string]any{}
	if value == "" {
		return fields, nil
	}
	if err := json.Unmarshal([]byte(value), &fields); err != nil || fields == nil {
		return nil, errors.New("reviewer endpoint must be an object")
	}
	if key, exists := fields["apiKey"]; exists {
		if _, ok := key.(string); !ok {
			return nil, errors.New("reviewer apiKey must be a secret reference string")
		}
	}
	return fields, nil
}
