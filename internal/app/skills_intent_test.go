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

func TestSessionsSkillTeachesPRCompletion(t *testing.T) {
	text := string(skillCatalog["ocman-sessions"])
	for _, guidance := range []string{
		"creating a pull request", "After creating a pull request", "`pull_requests`",
		`"action":"link"`, `"action":"linked"`, "current session",
		"compound platform ID", "before reporting completion", "PR URL",
		"Do not enable watching unless requested", "Do not guess",
		"another repository", "originating session", `"host":"github.com"`, `"repo":"other/project"`,
	} {
		if !strings.Contains(text, guidance) {
			t.Errorf("missing PR completion guidance: %s", guidance)
		}
	}
}
