package app

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestSkillsTeachToolIntent(t *testing.T) {
	examples := regexp.MustCompile("`(\\{[^`\n]*\\})`")
	for name, content := range skillCatalog {
		t.Run(name, func(t *testing.T) {
			text := string(content)
			for _, guidance := range []string{"Include a short, goal-focused `intent` on every", "Code Mode", "inline string literal"} {
				if !strings.Contains(text, guidance) {
					t.Errorf("missing intent guidance: %s", guidance)
				}
			}
			for _, match := range examples.FindAllStringSubmatch(text, -1) {
				var args map[string]any
				if err := json.Unmarshal([]byte(match[1]), &args); err != nil {
					t.Fatalf("invalid example %s: %v", match[1], err)
				}
				if _, ok := args["action"]; !ok {
					continue
				}
				if intent, ok := args["intent"].(string); !ok || strings.TrimSpace(intent) == "" {
					t.Errorf("tool example missing intent: %s", match[1])
				}
			}
		})
	}
}
