package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/configfile"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/testutil"
)

func TestHeadlessFileConfigMigrationAndAPISave(t *testing.T) {
	if runOcmanChild() {
		return
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	configPath := filepath.Join(dir, "config.yaml")
	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), "judge_model", "old/model"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteScript(t, filepath.Join(dir, "bin", "claude"), "#!/bin/sh\necho '2.1.220 (Claude Code)'\n")
	base, _, stop := startClaudeOcman(t, "TestHeadlessFileConfigMigrationAndAPISave", dir,
		"OCMAN_STATE_DB="+dbPath, "OCMAN_CONFIG="+configPath,
		"OCMAN_CONVERSATIONS_DB="+filepath.Join(dir, "state_conv.db"))
	defer stop()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := client.Get(base + "/api/settings/judge-model")
		if err == nil {
			var body struct{ Model string }
			decodeErr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && body.Model == "old/model" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("headless instance never served migrated reviewer setting")
		}
		time.Sleep(25 * time.Millisecond)
	}
	resp, err := client.Post(base+"/api/settings/judge-model", "application/json", strings.NewReader(`{"model":"new/model"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings save = %d", resp.StatusCode)
	}
	stop()
	doc, err := (&configfile.File{Path: configPath}).Read()
	if err != nil {
		t.Fatal(err)
	}
	if value, ok, err := doc.Get("settings", "judge_model"); err != nil || !ok || value != "new/model" {
		t.Fatalf("API did not save config: %q, %v", value, err)
	}
	backup, err := state.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if value, _, err := backup.GetSetting(t.Context(), "judge_model"); err != nil || value != "old/model" {
		t.Fatalf("original DB backup was changed: %q, %v", value, err)
	}
}
