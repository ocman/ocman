package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/configfile"
)

// ModelConfiguration is a saved preference, never a claimed run or live session.
// Raw is a compare-and-swap token so startup repair cannot overwrite an edit.
type ModelConfiguration struct {
	Kind, ID, Owner, Directory, Platform, Raw string
	Models                                    []string
}

func (d *DB) ModelConfigurations(ctx context.Context) ([]ModelConfiguration, error) {
	routines, err := d.ListRoutines(ctx, false)
	if err != nil {
		return nil, err
	}
	var out []ModelConfiguration
	for _, r := range routines {
		if r.SessionID != "" || r.SessionMode == "existing" {
			continue
		}
		raw, _ := json.Marshal(r)
		out = append(out, ModelConfiguration{Kind: "routine", ID: r.ID, Owner: r.RemoteID, Directory: r.Directory, Platform: r.Platform, Raw: string(raw), Models: []string{r.Model}})
	}
	settings, err := d.modelSettings(ctx)
	if err != nil {
		return nil, err
	}
	for key, raw := range settings {
		c := ModelConfiguration{Kind: "setting", ID: key, Owner: "local", Raw: raw}
		switch {
		case key == JudgeModelSettingKey:
			c.Models = []string{raw}
		case strings.HasPrefix(key, "project-defaults:"):
			var ownerDir []string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(key, "project-defaults:")), &ownerDir); err != nil || len(ownerDir) != 2 {
				continue
			}
			c.Owner, c.Directory = ownerDir[0], ownerDir[1]
			var fields struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				return nil, err
			}
			c.Models = []string{fields.Model}
		case strings.HasPrefix(key, "project:"):
			c.Directory = strings.TrimPrefix(key, "project:")
			var fields ProjectSettings
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				return nil, err
			}
			c.Models = fields.Models
		default:
			continue
		}
		out = append(out, c)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id, project_path, platform, models_json FROM factory_epic WHERE status != 'closed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := ModelConfiguration{Kind: "factory", Owner: "local"}
		if err := rows.Scan(&c.ID, &c.Directory, &c.Platform, &c.Raw); err != nil {
			return nil, err
		}
		var fields map[string]string
		if err := json.Unmarshal([]byte(c.Raw), &fields); err != nil {
			return nil, err
		}
		c.Models = []string{fields["plan"], fields["implementation"], fields["verification"]}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) modelSettings(ctx context.Context) (map[string]string, error) {
	keys := map[string]bool{JudgeModelSettingKey: true}
	rows, err := d.db.QueryContext(ctx, `SELECT key FROM setting WHERE key LIKE 'project:%' OR key LIKE 'project-defaults:%'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return nil, err
		}
		keys[key] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if f := d.fileConfig.Load(); f != nil {
		doc, err := f.file.Read()
		if err != nil {
			return nil, err
		}
		for _, key := range doc.Keys("settings") {
			keys[key] = true
		}
	}
	out := map[string]string{}
	for key := range keys {
		if key != JudgeModelSettingKey && !strings.HasPrefix(key, "project:") && !strings.HasPrefix(key, "project-defaults:") {
			continue
		}
		if _, _, managed := d.managedSettingValue(key); managed {
			continue
		}
		value, found, err := d.GetSetting(ctx, key)
		if err != nil {
			return nil, err
		}
		if found {
			out[key] = value
		}
	}
	return out, nil
}

func (d *DB) RepairModelConfiguration(ctx context.Context, c ModelConfiguration, platform string, models []string) (bool, error) {
	if len(models) != len(c.Models) {
		return false, fmt.Errorf("model configuration shape changed")
	}
	switch c.Kind {
	case "routine":
		var r Routine
		if err := json.Unmarshal([]byte(c.Raw), &r); err != nil {
			return false, err
		}
		result, err := d.db.ExecContext(ctx, `UPDATE routine SET platform=?, model=?, updated_at=?
   WHERE id=? AND updated_at=? AND platform=? AND model=? AND session_id='' AND session_mode!='existing' AND deleted=0`,
			platform, models[0], time.Now().UnixMilli(), c.ID, r.UpdatedAt, r.Platform, r.Model)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		return n != 0, err
	case "factory":
		raw, err := replaceModelFields(c.Raw, []string{"plan", "implementation", "verification"}, models)
		if err != nil {
			return false, err
		}
		result, err := d.db.ExecContext(ctx, `UPDATE factory_epic SET models_json=?, platform=? WHERE id=? AND models_json=? AND platform=? AND status!='closed'`, raw, platform, c.ID, c.Raw, c.Platform)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		return n != 0, err
	case "setting":
		raw := ""
		if len(models) > 0 {
			raw = models[0]
		}
		var err error
		if strings.HasPrefix(c.ID, "project:") {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.Raw), &fields); err != nil {
				return false, err
			}
			unique, seen := []string{}, map[string]bool{}
			for _, model := range models {
				if !seen[model] {
					unique = append(unique, model)
					seen[model] = true
				}
			}
			if err := ValidateProjectModels(unique); err != nil {
				return false, err
			}
			fields["models"], err = json.Marshal(unique)
			if err != nil {
				return false, err
			}
			encoded, err := json.Marshal(fields)
			if err != nil {
				return false, err
			}
			raw = string(encoded)
		} else if strings.HasPrefix(c.ID, "project-defaults:") {
			raw, err = replaceModelFields(c.Raw, []string{"model"}, models)
		}
		if err != nil {
			return false, err
		}
		return d.repairModelSetting(ctx, c.ID, c.Raw, raw)
	default:
		return false, fmt.Errorf("unknown model configuration")
	}
}

func replaceModelFields(raw string, keys, models []string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return "", err
	}
	for i, key := range keys {
		if models[i] != "" {
			fields[key], _ = json.Marshal(models[i])
		}
	}
	encoded, err := json.Marshal(fields)
	return string(encoded), err
}

func (d *DB) repairModelSetting(ctx context.Context, key, before, after string) (bool, error) {
	if err := d.rejectManagedWrite(key); err != nil {
		return false, err
	}
	if f := d.fileConfig.Load(); f != nil && isFileSetting(key) {
		changed := false
		err := f.file.Update(func(doc *configfile.Document) error {
			raw, found, err := doc.Get("settings", key)
			if err != nil || !found || raw != before {
				return err
			}
			if err := d.applyFileSetting(ctx, f, doc, key, after); err != nil {
				return err
			}
			changed = true
			return nil
		})
		return changed && err == nil, err
	}
	result, err := d.db.ExecContext(ctx, `UPDATE setting SET value=?, updated_at=? WHERE key=? AND value=?`, after, time.Now().UnixMilli(), key, before)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n != 0, err
}
