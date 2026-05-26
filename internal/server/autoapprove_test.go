package server

import (
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  judgeVerdict
	}{
		// JSON happy path.
		{
			"json safe",
			`{"verdict":"safe","reasoning":"Read-only operation.","risk_factors":[]}`,
			verdictSafe,
		},
		{
			"json unsafe",
			`{"verdict":"unsafe","reasoning":"Writes to .env file.","risk_factors":[".env"]}`,
			verdictUnsafe,
		},
		{
			"json uppercase verdict",
			`{"verdict":"SAFE","reasoning":"OK"}`,
			verdictSafe,
		},
		{
			"json with leading text",
			"Here is the result:\n" + `{"verdict":"safe","reasoning":"Fine.","risk_factors":[]}`,
			verdictSafe,
		},
		{
			"json in markdown fences",
			"```json\n{\"verdict\":\"unsafe\",\"reasoning\":\"Dangerous.\"}\n```",
			verdictUnsafe,
		},
		// Fallback keyword scan.
		{"bare SAFE", "SAFE", verdictSafe},
		{"bare UNSAFE", "UNSAFE", verdictUnsafe},
		{"lowercase safe fallback", "safe", verdictSafe},
		{"lowercase unsafe fallback", "unsafe", verdictUnsafe},
		{"SAFE with leading whitespace", "  SAFE  ", verdictSafe},
		{"explanation with UNSAFE", "This is UNSAFE because it modifies files.", verdictUnsafe},
		{"explanation with SAFE", "The action is SAFE — it only reads files.", verdictSafe},
		{"empty string defaults unsafe", "", verdictUnsafe},
		{"unrelated text defaults unsafe", "I cannot determine this.", verdictUnsafe},
		// UNSAFE contains SAFE as a substring — must detect UNSAFE first.
		{"UNSAFE keyword scan", "UNSAFE", verdictUnsafe},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVerdict(tt.input)
			if got != tt.want {
				t.Errorf("parseVerdict(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestJudgePromptCustomSections(t *testing.T) {
	// No custom sections — output should not contain "##" beyond the built-in ones.
	base := judgePrompt("read file", []string{"*.go"}, nil)
	if strings.Contains(base, "Feature branch") {
		t.Errorf("unexpected custom section in base prompt")
	}

	// One custom section.
	sections := []PromptSection{
		{Title: "Feature branch rule", Content: "git push to feature branches is SAFE."},
	}
	with := judgePrompt("git push", []string{"origin/feat"}, sections)
	if !strings.Contains(with, "## Feature branch rule") {
		t.Errorf("custom section title not found in prompt")
	}
	if !strings.Contains(with, "git push to feature branches is SAFE.") {
		t.Errorf("custom section content not found in prompt")
	}

	// Empty title and content are skipped — no new sections added beyond base.
	base2 := judgePrompt("git push", nil, nil)
	empty := judgePrompt("git push", nil, []PromptSection{{Title: "", Content: ""}})
	if empty != base2 {
		t.Errorf("empty section should produce identical output to no sections")
	}

	// Blank title falls back to "Additional rule".
	noTitle := judgePrompt("git push", nil, []PromptSection{{Title: "", Content: "some rule"}})
	if !strings.Contains(noTitle, "## Additional rule") {
		t.Errorf("blank title should fall back to 'Additional rule'")
	}
}
