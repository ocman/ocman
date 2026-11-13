package autoapprove

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestTypeSafeContextPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, directory, message, reason string
		historyErr, policyErr            error
		wantCall                         bool
	}{
		{name: "exact limit", directory: "/repo", message: strings.Repeat("x", 32_000), wantCall: true},
		{name: "over limit", directory: "/repo", message: strings.Repeat("x", 32_001), wantCall: true},
		{name: "UTF-8 exact limit", directory: "/repo", message: strings.Repeat("é", 16_000), wantCall: true},
		{name: "UTF-8 over limit", directory: "/repo", message: strings.Repeat("é", 16_001), wantCall: true},
		{name: "missing project", message: "Inspect files", reason: "Project directory"},
		{name: "empty history", directory: "/repo", reason: "No user messages"},
		{name: "failed history", directory: "/repo", historyErr: errors.New("SECRET internal failure"), reason: "User history could not be loaded"},
		{name: "history timeout", directory: "/repo", historyErr: context.DeadlineExceeded, reason: "User history refresh timed out"},
		{name: "failed policy", directory: "/repo", message: "Inspect files", policyErr: errors.New("SECRET policy failure"), reason: "Reviewer policy could not be loaded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(typeSafeTestResponse(nil, nil)))
			}))
			defer endpoint.Close()
			store := &ruleContextStore{metricsStore: metricsStore{value: `{"format":"typesafe","endpoint":"` + endpoint.URL + `","minSafeProbability":0.99}`}, sectionErr: tc.policyErr}
			s := NewService(Deps{Store: store, JudgeUserMessages: func(context.Context, platforms.ID, string) (JudgeUserContext, error) {
				if tc.message == "" {
					return JudgeUserContext{}, tc.historyErr
				}
				return JudgeUserContext{Messages: []string{tc.message}}, tc.historyErr
			}})
			sections := s.resolveJudgeSections(t.Context(), &fakePlatform{id: "test"}, tc.directory, "session")
			config, err := LoadJudgeEndpoint(t.Context(), store)
			if err != nil {
				t.Fatal(err)
			}
			result := judgeEndpoint(t.Context(), config, tc.directory, "read", nil, nil, sections)
			if tc.wantCall {
				if calls.Load() != 1 || result.Verdict != verdictSafe {
					t.Fatalf("calls=%d result=%+v", calls.Load(), result)
				}
			} else if calls.Load() != 0 || result.Verdict != verdictUnsafe || !result.EvaluationFailed || !strings.Contains(result.Reasoning, tc.reason) || strings.Contains(result.Reasoning, "SECRET") {
				t.Fatalf("calls=%d result=%+v; want reason %q without an HTTP request", calls.Load(), result, tc.reason)
			}
		})
	}
}

func TestTypeSafeContextCountsAllMessages(t *testing.T) {
	sections := []PromptSection{{userIntent: true, messages: []string{strings.Repeat("x", 16_000), strings.Repeat("é", 8_000)}}}
	if _, issue := typeSafeContext("/repo", sections); issue != "" {
		t.Fatal(issue)
	}
	sections[0].messages = append(sections[0].messages, "x")
	if userContext, issue := typeSafeContext("/repo", sections); !userContext.Limited || issue != "" || len(strings.Join(userContext.Messages, "")) > MaxJudgeUserMessageBytes {
		t.Fatalf("unbounded combined history: %+v %s", userContext, issue)
	}
}
