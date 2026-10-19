package autoapprove

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHostedTypeSafe(t *testing.T) {
	for _, tc := range []struct {
		endpoint, format, key string
		want                  bool
	}{
		{"https://api.typesafe.ai/v1/systemone", "typesafe", "secret", true},
		{"https://api.typesafe.ai/v1/systemone", "typesafe", "", false},
		{"https://api.typesafe.ai/v1/systemone", "typesafe", "  ", false},
		{"https://api.typesafe.ai/v1/systemone", "openai", "secret", false},
		{"http://api.typesafe.ai/v1/systemone", "typesafe", "secret", false},
		{"https://api.typesafe.ai.evil/v1/systemone", "typesafe", "secret", false},
		{"https://user@api.typesafe.ai/v1/systemone", "typesafe", "secret", false},
		{"https://api.typesafe.ai/custom", "typesafe", "secret", false},
		{"http://localhost:8000/v1/systemone", "typesafe", "secret", false},
		{"://", "typesafe", "secret", false},
	} {
		if got := (JudgeEndpoint{Endpoint: tc.endpoint, Format: tc.format, APIKey: tc.key}).HostedTypeSafe(); got != tc.want {
			t.Errorf("%s %s: hosted = %v", tc.endpoint, tc.format, got)
		}
	}
}

func TestHostedTypeSafeUsageThroughJudge(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name, body    string
		status, calls int
		input         *int64
	}{
		{"valid", `{"model":"jev-1.14.0","usage":{"input_tokens":100,"output_tokens":5},"answers":{"verdict":{"choice":"safe","probabilities":{"safe":1,"unsafe":0,"uncertain":0}}}}`, 200, 1, new(int64(100))},
		{"invalid verdict still billed", `{"model":"jev-1.14.0","usage":{"input_tokens":100}}`, 200, 1, new(int64(100))},
		{"missing usage", `{"model":"jev-1.14.0"}`, 200, 1, nil},
		{"malformed", `{`, 200, 1, nil},
		{"rejected", `{}`, 401, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://api.typesafe.ai/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("unexpected endpoint or authentication")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})
			config, _ := json.Marshal(JudgeEndpoint{Format: "typesafe", Endpoint: "https://api.typesafe.ai/v1/systemone", APIKey: "test-secret", MinSafeProbability: .99})
			store := &metricsStore{value: string(config)}
			calls := 0
			s := NewService(Deps{Store: store, OpenCodeDisabled: true, RecordTypeSafeUsage: func(_ context.Context, model string, input, output *int64) error {
				calls++
				if (input == nil) != (tc.input == nil) || input != nil && *input != *tc.input {
					t.Errorf("input = %v", input)
				}
				if tc.name == "valid" && (model != "jev-1.14.0" || output == nil || *output != 5) {
					t.Errorf("usage model=%s output=%v", model, output)
				}
				return nil
			}})
			s.judgePermission(t.Context(), "opencode", "/project", "read", nil, nil, nil, nil)
			if calls != tc.calls {
				t.Fatalf("recorded calls = %d, want %d", calls, tc.calls)
			}
		})
	}
}

func TestTypeSafeUsageRecorderGatesAndFailure(t *testing.T) {
	config := JudgeEndpoint{Format: "typesafe", Endpoint: "https://api.typesafe.ai/v1/systemone", APIKey: "secret"}
	j := &PermissionJudge{}
	usage := readTypeSafeUsage([]byte(`{"model":"jev-1.13.0","usage":{"input_tokens":0}}`))
	j.recordTypeSafeUsage(t.Context(), config, usage) // Optional recorder.
	calls := 0
	j.recordUsage = func(ctx context.Context, _ string, _, _ *int64) error {
		calls++
		if ctx.Err() != nil {
			t.Error("accounting inherited cancellation")
		}
		return errors.New("database unavailable")
	}
	j.recordTypeSafeUsage(t.Context(), config, nil)
	custom := config
	custom.Endpoint = "http://localhost/v1/systemone"
	j.recordTypeSafeUsage(t.Context(), custom, usage)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	j.recordTypeSafeUsage(ctx, config, usage)
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}
