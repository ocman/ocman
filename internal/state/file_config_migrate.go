package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ocman/ocman/internal/configfile"
)

// migrateFileConfig is a startup data migration, not a SQLite schema migration:
// publishing files inside a SQL transaction would not be atomic. Source rows
// remain untouched, and a complete file is published only after secret slots
// have been committed. Restart safely retries an interrupted migration.
func (d *DB) migrateFileConfig(ctx context.Context) (*configfile.Document, error) {
	doc, err := configfile.Parse([]byte("# Managed by ocman and your configuration tools.\nversion: 1\nsettings: {}\n"))
	if err != nil {
		return nil, err
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT key, value FROM setting ORDER BY key`)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return nil, err
		}
		if isFileSetting(key) {
			values[key] = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		var next any = value
		switch {
		case isFileSecret(key) && value != "":
			if key == "preview_apps" {
				sealed, decodeErr := base64.RawStdEncoding.DecodeString(value)
				if decodeErr != nil {
					return nil, fmt.Errorf("decoding legacy secret setting %s", key)
				}
				value, err = d.decryptToken(ctx, sealed)
				if err != nil {
					return nil, fmt.Errorf("decrypting legacy secret setting %s", key)
				}
			}
			next, err = d.storeConfigSecret(ctx, value)
		case key == "judge_endpoint":
			var fields map[string]any
			fields, err = endpointFields(value)
			if err == nil {
				if secret, _ := fields["apiKey"].(string); secret != "" {
					fields["apiKey"], err = d.storeConfigSecret(ctx, secret)
				}
				next = fields
			}
		default:
			var structured any
			if json.Unmarshal([]byte(value), &structured) == nil {
				switch structured.(type) {
				case map[string]any, []any:
					next = structured
				}
			}
		}
		if err != nil {
			return nil, err
		}
		if err := doc.Set("settings", key, next); err != nil {
			return nil, err
		}
	}
	sections, err := d.GetPromptSections(ctx)
	if err != nil {
		return nil, err
	}
	// Convert through JSON to retain the public field names, not Go YAML's
	// default lowercasing of struct fields.
	b, err := json.Marshal(sections)
	if err != nil {
		return nil, err
	}
	var sectionValues any
	if err := json.Unmarshal(b, &sectionValues); err != nil {
		return nil, err
	}
	if err := doc.Set("settings", managedPromptSectionsKey, sectionValues); err != nil {
		return nil, err
	}
	delay, err := d.GetJudgeDelayMs(ctx)
	if err != nil {
		return nil, err
	}
	if err := doc.Set("settings", managedJudgeDelayKey, delay); err != nil {
		return nil, err
	}
	return doc, nil
}
