package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileConfigPromptSectionsKeepCommentsOnSave(t *testing.T) {
	db := openTestStateDB(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "version: 1\nsettings:\n  prompt_sections:\n    # section note\n    - title: policy # title note\n      content: keep this # content note\n      enabled: false # enabled note\n"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	sections, err := db.GetPromptSections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, edited := range []bool{false, true} {
		if edited {
			sections[0].Content = "updated policy"
		}
		if err := db.SetPromptSections(t.Context(), sections); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, comment := range []string{"# section note", "# title note", "# content note", "# enabled note"} {
			if !strings.Contains(string(data), comment) {
				t.Errorf("save lost %s, edited=%t", comment, edited)
			}
		}
	}
}
