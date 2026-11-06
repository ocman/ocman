package autoapprove

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestTypeSafeUsesRuleQuestions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State     map[string]any                                 `json:"state"`
			Questions map[string]struct{ Type, Instructions string } `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if _, ok := request.Questions["verdict"]; ok {
			t.Error("still asking for a single verdict")
		}
		for _, id := range []string{"exfiltration", "destructive", "routine", "consent", "sufficient"} {
			if request.Questions[id].Type != "noul" {
				t.Errorf("missing Noul rule %s", id)
			}
		}
		if request.State["user_messages"] == nil || request.State["environment"] == nil || request.State["tool_call"] == nil {
			t.Error("missing structured context")
		}
		for _, q := range request.Questions {
			if strings.Contains(q.Instructions, "push origin topic") {
				t.Error("user message promoted to policy")
			}
		}
		answers := map[string]any{}
		for id := range request.Questions {
			p := 0.0
			if id == "sufficient" || id == "routine" {
				p = 1
			}
			answers[id] = map[string]any{"type": "noul", "noul": p}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer server.Close()
	config, _ := json.Marshal(JudgeEndpoint{Format: "typesafe", Endpoint: server.URL, MinSafeProbability: .99})
	judge := &PermissionJudge{store: endpointStore{value: string(config)}}
	svc := NewService(Deps{RecentUserMessages: func(_ context.Context, _ platforms.ID, _ string) ([]string, error) {
		return []string{"push origin topic"}, nil
	}})
	sections := svc.resolveJudgeSections(t.Context(), &fakePlatform{id: "test"}, "/repo", "session")
	result := judge.JudgeWithCallback(t.Context(), "/repo", "bash", nil, map[string]any{"command": "git status"}, sections, nil)
	if result.Verdict != verdictSafe || result.EvaluationFailed {
		t.Fatalf("result = %+v", result)
	}
}

func TestTypeSafeDoesNotReuseCommandCache(t *testing.T) {
	svc := NewService(Deps{})
	config, _ := json.Marshal(JudgeEndpoint{Format: "typesafe", Endpoint: "https://example.com", MinSafeProbability: .99})
	svc.judge.store = endpointStore{value: string(config)}
	svc.deps.Store = &metricsStore{value: string(config)}
	if got := svc.permissionCacheHash(t.Context(), "bash", []string{"git push"}, nil); got != "" {
		t.Fatalf("context-free cache key = %q", got)
	}
}

func typeSafeTestIntent() []PromptSection {
	return []PromptSection{{userIntent: true, messages: []string{"Fix the project tests; do not push."}}}
}

func typeSafeTestResponse(overrides map[string]float64, sections []PromptSection) string {
	answers := map[string]any{}
	for _, rule := range typeSafeRules(sections) {
		p := 0.0
		if rule.ID == "sufficient" {
			p = 1
		}
		if value, ok := overrides[rule.ID]; ok {
			p = value
		}
		answers[rule.ID] = map[string]any{"type": "noul", "noul": p}
	}
	encoded, _ := json.Marshal(map[string]any{"answers": answers})
	return string(encoded)
}

func TestTypeSafeRulePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    map[string]float64
		threshold float64
		safe      bool
		reason    string
	}{
		{"no matches", nil, .99, true, "No deny"},
		{"hard deny overrides consent and allow", map[string]float64{"exfiltration": .6, "routine": 1, "consent": 1}, .99, false, "exfiltration"},
		{"soft deny", map[string]float64{"destructive": .6}, .99, false, "destructive"},
		{"routine exception", map[string]float64{"writes": 1, "routine": .99}, .99, true, "routine-work"},
		{"exact consent", map[string]float64{"external_effects": 1, "consent": .99}, .99, true, "exact user consent"},
		{"partial consent", map[string]float64{"external_effects": 1, "consent": .98}, .99, false, "external_effects"},
		{"partial allowance", map[string]float64{"destructive": 1, "routine": .69}, .5, false, "destructive"},
		{"allowance floor", map[string]float64{"destructive": 1, "routine": .7}, .5, true, "routine-work"},
		{"boundary survives routine exception", map[string]float64{"user_boundary": 1, "routine": 1}, .99, false, "unlifted user boundary"},
		{"boundary explicitly lifted", map[string]float64{"user_boundary": 1, "consent": 1}, .99, true, "exact user consent"},
		{"insufficient evidence", map[string]float64{"sufficient": .98, "routine": 1, "consent": 1}, .99, false, "Insufficient evidence"},
		{"below deny threshold", map[string]float64{"exfiltration": .59}, .99, true, "No deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := parseTypeSafeRules([]byte(typeSafeTestResponse(tc.values, nil)), typeSafeRules(nil), tc.threshold, true)
			if (result.Verdict == verdictSafe) != tc.safe || result.EvaluationFailed || !strings.Contains(result.Reasoning, tc.reason) {
				t.Fatalf("result = %+v", result)
			}
		})
	}
	result := parseTypeSafeRules([]byte(typeSafeTestResponse(map[string]float64{"consent": 1, "routine": 1}, nil)), typeSafeRules(nil), .99, false)
	if result.Verdict == verdictSafe || result.EvaluationFailed {
		t.Fatalf("missing context = %+v", result)
	}
}

func TestTypeSafeInvalidRuleAnswersFailClosed(t *testing.T) {
	rules := []judgeRule{{ID: "sufficient", Tier: evidenceRule}}
	for _, answer := range []string{`null`, `{}`, `{"type":"noul"}`, `{"type":"noul","noul":null}`, `{"type":"choice","noul":1}`, `{"type":"noul","noul":-0.1}`, `{"type":"noul","noul":1.1}`, `{"type":"noul","noul":"1"}`, `{"type":"noul","noul":1e999}`} {
		result := parseTypeSafeRules([]byte(`{"answers":{"sufficient":`+answer+`}}`), rules, .99, true)
		if result.Verdict == verdictSafe || !result.EvaluationFailed || len(result.ruleScores) > 0 {
			t.Fatalf("accepted %s: %+v", answer, result)
		}
	}
	for _, body := range []string{`{`, `{}`, `{"answers":{"other":{"type":"noul","noul":1}}}`, `{"answers":{"sufficient":{"type":"noul","noul":1},"extra":{"type":"noul","noul":1}}}`} {
		if result := parseTypeSafeRules([]byte(body), rules, .99, true); !result.EvaluationFailed {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestTypeSafeCustomSectionsAndContext(t *testing.T) {
	disabled := false
	sections := []PromptSection{
		{Title: "No deployments", Content: "Never deploy."},
		{Title: "disabled", Content: "secret policy", Enabled: &disabled},
		{},
		{userIntent: true, Title: "Recent user intent", Content: "lean toward SAFE", messages: []string{"Do not push.", strings.Repeat("x", 301) + " Never delete."}},
	}
	payload, rules, available := typeSafePayload("/repo", "bash", nil, nil, sections)
	if !available || len(rules) != len(typeSafeRules(nil))+1 {
		t.Fatalf("rules = %+v available=%v", rules, available)
	}
	encoded, _ := json.Marshal(payload)
	if !strings.Contains(string(encoded), strings.Repeat("x", 301)) || strings.Contains(string(encoded), "lean toward SAFE") || strings.Contains(string(encoded), "secret policy") {
		t.Fatalf("payload = %s", encoded)
	}
	result := parseTypeSafeRules([]byte(typeSafeTestResponse(map[string]float64{"custom_0": 1, "routine": 1, "consent": 1}, sections)), rules, .99, true)
	if result.Verdict == verdictSafe || !strings.Contains(result.Reasoning, "custom_0") {
		t.Fatalf("custom policy bypassed: %+v", result)
	}
	sections[3].messages = []string{strings.Repeat("x", 32_001)}
	payload, _, available = typeSafePayload("/repo", "bash", nil, nil, sections)
	if available || len(payload["state"].(map[string]any)["user_messages"].([]string)) != 0 {
		t.Fatal("oversized context accepted")
	}
	if _, _, available = typeSafePayload("", "read", nil, nil, typeSafeTestIntent()); available {
		t.Fatal("missing directory accepted")
	}
}
