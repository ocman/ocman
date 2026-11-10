package projectforge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/state/statetest"
)

func TestFileConfigPromptTemplatesPreserveExactText(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"object", `{"id":9007199254740993,"body":"{body}"}`},
		{"array", "[9007199254740993,\n  \"{number}\", \"\\u0061\"]\n"},
		{"whitespace", " { \"body\": \"{body}\", \"id\": 9007199254740993 } \n"},
		{"empty array", "[]"}, {"empty object", "{}"}, {"null", "null"}, {"false", "false"}, {"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := tc.text
			store := statetest.Open(t)
			want := promptTemplates{PR: text, Issue: text, Review: text}
			for _, key := range []string{settingPRPromptTemplate, settingIssuePromptTemplate, settingReviewPromptTemplate} {
				if err := store.SetSetting(t.Context(), key, text); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.EnableFileConfig(t.Context(), filepath.Join(t.TempDir(), "config.yaml")); err != nil {
				t.Fatal(err)
			}
			m := newTestModule()
			m.Store = store
			check := func(method string, body []byte) {
				t.Helper()
				req := httptest.NewRequest(method, "/api/settings/prompt-templates", bytes.NewReader(body))
				rr := httptest.NewRecorder()
				m.handlePromptTemplates(rr, req)
				var got promptTemplates
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &got) != nil || got != want {
					t.Errorf("%s changed template text: status=%d body=%s", method, rr.Code, rr.Body.String())
				}
			}
			check(http.MethodGet, nil)
			payload, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			check(http.MethodPost, payload)
			check(http.MethodGet, nil)
			got, err := m.promptTemplateFor(t.Context(), "pr", "handle")
			if err != nil || got != text {
				t.Errorf("render source changed template text: got=%q, err=%v", got, err)
			}
		})
	}
}
