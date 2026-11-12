package state

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestModelConfigurationRepairPreservesFileSettingsAndConcurrentEdits(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "database", true: "file"}[file], func(t *testing.T) {
			db := openTestStateDB(t)
			if file {
				if err := db.EnableFileConfig(t.Context(), filepath.Join(t.TempDir(), "config.json")); err != nil {
					t.Fatal(err)
				}
			}
			key := `project-defaults:["local","/repo"]`
			raw := `{"model":"p/m","permissionMode":"plan","extra":{"keep":true}}`
			if err := db.SetSetting(t.Context(), key, raw); err != nil {
				t.Fatal(err)
			}
			configs, err := db.ModelConfigurations(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var saved ModelConfiguration
			for _, c := range configs {
				if c.ID == key {
					saved = c
				}
			}
			if saved.Owner != "local" || saved.Directory != "/repo" {
				t.Fatalf("config = %+v", saved)
			}
			if ok, err := db.RepairModelConfiguration(t.Context(), saved, "", []string{"cnx_work:p/m"}); err != nil || !ok {
				t.Fatalf("repair = %v, %v", ok, err)
			}
			got, _, _ := db.GetSetting(t.Context(), key)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(got), &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["model"]) != `"cnx_work:p/m"` || string(fields["permissionMode"]) != `"plan"` || string(fields["extra"]) != `{"keep":true}` {
				t.Fatalf("lost fields: %s", got)
			}
			if ok, err := db.RepairModelConfiguration(t.Context(), saved, "", []string{"cnx_other:p/m"}); err != nil || ok {
				t.Fatalf("overwrote concurrent edit: %v, %v", ok, err)
			}
		})
	}
}

func TestModelConfigurationRepairFactoryAndJudge(t *testing.T) {
	db := openTestStateDB(t)
	ctx := t.Context()
	if _, err := db.SQL().ExecContext(ctx, `INSERT INTO factory_project(path,created_at) VALUES('/repo',1);
 INSERT INTO factory_epic(id,project_path,status,goal,created_at,updated_at,platform,models_json) VALUES('epic','/repo','open','Goal',1,1,'opencode','{"plan":"p/m","implementation":"p/m","extra":"keep"}')`); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, JudgeModelSettingKey, "p/m"); err != nil {
		t.Fatal(err)
	}
	configs, err := db.ModelConfigurations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range configs {
		models := append([]string(nil), c.Models...)
		for i, model := range models {
			if model != "" {
				models[i] = "cnx_work:" + model
			}
		}
		if ok, err := db.RepairModelConfiguration(ctx, c, "cnx_work", models); err != nil || !ok {
			t.Fatalf("repair %s = %v, %v", c.ID, ok, err)
		}
	}
	if got, _, _ := db.GetSetting(ctx, JudgeModelSettingKey); got != "cnx_work:p/m" {
		t.Fatal(got)
	}
	var raw, platform string
	if err := db.SQL().QueryRowContext(ctx, `SELECT models_json,platform FROM factory_epic WHERE id='epic'`).Scan(&raw, &platform); err != nil {
		t.Fatal(err)
	}
	var models map[string]string
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		t.Fatal(err)
	}
	if platform != "cnx_work" || models["extra"] != "keep" || models["plan"] != "cnx_work:p/m" || models["implementation"] != "cnx_work:p/m" {
		t.Fatalf("factory = %s %s", platform, raw)
	}
}
