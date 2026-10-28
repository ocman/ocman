package projectforge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state/statetest"
)

func TestHandleGetPromptTemplates_DefaultsWhenUnset(t *testing.T) {
	m := newStoreModule(t)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/prompt-templates", nil)
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got promptTemplates
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PR != DefaultPRPromptTemplate {
		t.Errorf("PR default mismatch:\n got: %q\nwant: %q", got.PR, DefaultPRPromptTemplate)
	}
	if got.Issue != DefaultIssuePromptTemplate {
		t.Errorf("Issue default mismatch")
	}
	if got.Review != DefaultReviewPromptTemplate {
		t.Errorf("Review default mismatch")
	}
}

func TestHandleSetPromptTemplates_ReviewPersistsAndRenders(t *testing.T) {
	m := newStoreModule(t)

	body := `{"review": "REVIEW #{number}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}

	// promptTemplateFor with action=review must return the persisted value.
	got, err := m.promptTemplateFor(t.Context(), "pr", "review")
	if err != nil {
		t.Fatalf("promptTemplateFor: %v", err)
	}
	if got != "REVIEW #{number}" {
		t.Errorf("review template not persisted, got %q", got)
	}
	// action=handle still returns the PR (handle) template default.
	if got, _ := m.promptTemplateFor(t.Context(), "pr", "handle"); got != DefaultPRPromptTemplate {
		t.Errorf("handle action should use PR template, got %q", got)
	}
	// review action for an issue falls back to the issue template.
	if got, _ := m.promptTemplateFor(t.Context(), "issue", "review"); got != DefaultIssuePromptTemplate {
		t.Errorf("issue+review should use issue template, got %q", got)
	}
}

func TestHandleSetPromptTemplates_PersistsAndReturns(t *testing.T) {
	m := newStoreModule(t)

	body := `{"pr": "PR template here", "issue": "Issue template here"}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got promptTemplates
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PR != "PR template here" || got.Issue != "Issue template here" {
		t.Errorf("response mismatch: %+v", got)
	}

	// Read-back: GET returns the persisted values, not the defaults.
	req2 := httptest.NewRequest(http.MethodGet, "/api/settings/prompt-templates", nil)
	rr2 := httptest.NewRecorder()
	m.handlePromptTemplates(rr2, req2)
	var got2 promptTemplates
	if err := json.Unmarshal(rr2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if got2.PR != "PR template here" {
		t.Errorf("read-back PR: %q", got2.PR)
	}
}

func TestHandleSetPromptTemplates_PartialUpdateKeepsOther(t *testing.T) {
	m := newStoreModule(t)

	// First, set both.
	post := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		m.handlePromptTemplates(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
		}
	}
	post(`{"pr": "first-pr", "issue": "first-issue"}`)
	// Update only PR; issue should remain.
	post(`{"pr": "second-pr"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/prompt-templates", nil)
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, req)
	var got promptTemplates
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PR != "second-pr" {
		t.Errorf("expected updated PR, got %q", got.PR)
	}
	if got.Issue != "first-issue" {
		t.Errorf("expected preserved Issue, got %q", got.Issue)
	}
}

func TestHandleSetPromptTemplates_EmptyStringIsExplicitOverride(t *testing.T) {
	m := newStoreModule(t)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates",
		bytes.NewBufferString(`{"pr": ""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}

	// GET should now return the empty string (NOT the default), since
	// the user explicitly set it. This matches the FR-10 wording: the
	// stored value wins over defaults when present.
	req2 := httptest.NewRequest(http.MethodGet, "/api/settings/prompt-templates", nil)
	rr2 := httptest.NewRecorder()
	m.handlePromptTemplates(rr2, req2)
	var got promptTemplates
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PR != "" {
		t.Errorf("expected empty PR (explicit override), got %q", got.PR)
	}
}

func TestHandlePromptTemplates_RejectsOtherMethods(t *testing.T) {
	m := newStoreModule(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/settings/prompt-templates", nil)
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestIssueBranchName_VariousTitles(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"", "issue/7"},
		{"Simple title", "issue/7-simple-title"},
		{"  Padded ", "issue/7-padded"},
		{"!!!", "issue/7"},
		{"Make the foo do the bar", "issue/7-make-the-foo-do-the-bar"},
		// Slug is capped at 40 chars (not counting the "issue/<n>-" prefix);
		// trailing partial words become a partial slug + trim.
		{"Long title with many many many many many many many words to truncate", "issue/7-long-title-with-many-many-many-many-many"},
	}
	for _, tc := range cases {
		got := issueBranchName(7, tc.title)
		if got != tc.want {
			t.Errorf("issueBranchName(7, %q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func newStoreModule(t *testing.T) *Module {
	t.Helper()
	store := statetest.Open(t)
	m := newTestModule()
	m.Store = store
	return m
}

func TestPromptTemplatesWithoutStore(t *testing.T) {
	m := newTestModule()
	rr := httptest.NewRecorder()
	m.handlePromptTemplates(rr, httptest.NewRequest(http.MethodGet, "/api/settings/prompt-templates", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Please handle PR") {
		t.Fatalf("defaults: status = %d, body = %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	m.handlePromptTemplates(rr, httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates", bytes.NewBufferString(`{"pr":"x"}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("save without store: status = %d", rr.Code)
	}
	if got, err := m.promptTemplateFor(t.Context(), "issue", ""); err != nil || got != DefaultIssuePromptTemplate {
		t.Fatalf("template without store = %q, %v", got, err)
	}
}
