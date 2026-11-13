package autoapprove

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func TestTypeSafeLimitedContextCanGrant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]float64
		safe   bool
	}{
		{"routine grant", map[string]float64{"writes": 1, "routine": 1}, true},
		{"explicit consent grant", map[string]float64{"external_effects": 1, "consent": 1}, true},
		{"hard deny still wins", map[string]float64{"exfiltration": 1, "consent": 1, "routine": 1}, false},
		{"visible boundary still wins", map[string]float64{"user_boundary": 1, "routine": 1}, false},
		{"low confidence still blocks", map[string]float64{"writes": 1, "routine": .98}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				var request struct {
					State struct {
						Messages []string `json:"user_messages"`
						Complete *bool    `json:"user_messages_complete"`
					} `json:"state"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				bytes := 0
				for _, text := range request.State.Messages {
					bytes += len(text)
					if !utf8.ValidString(text) {
						t.Error("invalid UTF-8")
					}
				}
				if bytes > 32_000 || request.State.Complete == nil || *request.State.Complete || len(request.State.Messages) == 0 || !strings.HasSuffix(request.State.Messages[len(request.State.Messages)-1], "Run tests.") {
					t.Errorf("unexpected limited context: bytes=%d complete=%v", bytes, request.State.Complete)
				}
				_, _ = w.Write([]byte(typeSafeTestResponse(tc.values, nil)))
			}))
			defer server.Close()
			sections := []PromptSection{{userIntent: true, messages: []string{strings.Repeat("é", 17_000) + "Run tests."}}}
			result := judgeEndpoint(t.Context(), JudgeEndpoint{Format: "typesafe", Endpoint: server.URL, MinSafeProbability: .99}, "/repo", "bash", nil, nil, sections)
			if !called.Load() || (result.Verdict == verdictSafe) != tc.safe || result.EvaluationFailed || !strings.Contains(result.Reasoning, "Limited-context review") {
				t.Fatalf("called=%v result=%+v", called.Load(), result)
			}
		})
	}
}
