package autoapprove

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

type endpointStore struct {
	value string
	err   error
}

func (s endpointStore) GetSetting(context.Context, string) (string, bool, error) {
	return s.value, s.value != "", s.err
}

func TestJudgeEndpointConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config JudgeEndpoint
		valid  bool
	}{
		{"disabled", JudgeEndpoint{MinSafeProbability: .99}, true},
		{"local", JudgeEndpoint{Format: "openai", Endpoint: "http://127.0.0.1:8080/v1/chat/completions", Model: "local", MinSafeProbability: .99}, true},
		{"typesafe", JudgeEndpoint{Format: "typesafe", Endpoint: "https://api.typesafe.ai/v1/systemone", MinSafeProbability: .99}, true},
		{"unknown format", JudgeEndpoint{Format: "other", MinSafeProbability: .99}, false},
		{"relative", JudgeEndpoint{Format: "typesafe", Endpoint: "/v1/systemone", MinSafeProbability: .99}, false},
		{"scheme", JudgeEndpoint{Format: "typesafe", Endpoint: "file:///tmp/model", MinSafeProbability: .99}, false},
		{"credentials", JudgeEndpoint{Format: "typesafe", Endpoint: "https://user:key@example.com/v1", MinSafeProbability: .99}, false},
		{"fragment", JudgeEndpoint{Format: "typesafe", Endpoint: "https://example.com/v1#key", MinSafeProbability: .99}, false},
		{"missing model", JudgeEndpoint{Format: "openai", Endpoint: "http://localhost/v1", MinSafeProbability: .99}, false},
		{"low threshold", JudgeEndpoint{MinSafeProbability: .49}, false},
		{"high threshold", JudgeEndpoint{MinSafeProbability: 1.01}, false},
		{"header injection", JudgeEndpoint{APIKey: "key\nInjected: true", MinSafeProbability: .99}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.config.Validate() == nil; got != tc.valid {
				t.Fatalf("valid = %v", got)
			}
		})
	}
	for _, store := range []judgeModelStore{nil, endpointStore{}} {
		c, err := LoadJudgeEndpoint(t.Context(), store)
		if err != nil || c.Format != "" || c.MinSafeProbability != .99 {
			t.Fatalf("default = %+v, %v", c, err)
		}
	}
	for _, store := range []endpointStore{{value: "{"}, {value: `{"format":"invalid"}`}, {err: errors.New("offline")}} {
		if _, err := LoadJudgeEndpoint(t.Context(), store); err == nil {
			t.Fatal("expected load failure")
		}
	}
}

func TestCustomJudgeBypassesOpenCode(t *testing.T) {
	for _, format := range []string{"openai", "typesafe"} {
		t.Run(format, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/custom" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected request: %s %s %v", r.Method, r.URL, r.Header)
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request["model"] != "test-model" {
					t.Errorf("model = %v", request["model"])
				}
				encoded, _ := json.Marshal(request)
				for _, text := range []string{"git status", "Extra policy", "Only inspect this repository"} {
					if !strings.Contains(string(encoded), text) {
						t.Errorf("missing %q in %s", text, encoded)
					}
				}
				if format == "openai" {
					if request["stream"] != false || request["messages"] == nil {
						t.Error("missing chat fields")
					}
					_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"verdict\":\"safe\",\"reasoning\":\"Read-only.\"}"}}]}`)
				} else {
					if request["state"] == nil || request["questions"] == nil {
						t.Error("missing System One fields")
					}
					_, _ = io.WriteString(w, `{"answers":{"verdict":{"choice":"safe","probabilities":{"safe":0.995,"unsafe":0.003,"uncertain":0.002}}}}`)
				}
			}))
			defer server.Close()
			config := JudgeEndpoint{Format: format, Endpoint: server.URL + "/custom", Model: "test-model", APIKey: "test-key", MinSafeProbability: .99}
			value, _ := json.Marshal(config)
			judge := &PermissionJudge{store: endpointStore{value: string(value)}, openCodePort: func(string) string { t.Error("OpenCode must not be contacted"); return "" }}
			checking := false
			result := judge.JudgeWithCallback(t.Context(), "", "bash", []string{"git *"}, map[string]any{"command": "git status"}, []PromptSection{{Title: "Extra policy", Content: "Only inspect this repository"}}, func(id string) {
				checking = true
				if id != "" {
					t.Error("external check has no transient session")
				}
			})
			if !checking {
				t.Error("checking callback was not invoked")
			}
			if result.Verdict != verdictSafe || result.EvaluationFailed || result.Reasoning == "" {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestEndpointFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"http error", `secret-provider-error`, 401},
		{"redirect", ``, 307},
		{"bad json", `{`, 200},
		{"missing choices", `{}`, 200},
		{"truncated", `{"choices":[{"finish_reason":"length","message":{"content":"{\"verdict\":\"safe\"}"}}]}`, 200},
		{"refusal", `{"choices":[{"finish_reason":"stop","message":{"refusal":"refused","content":"{\"verdict\":\"safe\"}"}}]}`, 200},
		{"invalid verdict", `{"choices":[{"finish_reason":"stop","message":{"content":"SAFE"}}]}`, 200},
		{"oversize", strings.Repeat("x", (1<<20)+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected authentication")
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			config := JudgeEndpoint{Format: "openai", Endpoint: server.URL, Model: "local", MinSafeProbability: .99}
			result := judgeEndpoint(t.Context(), config, "read", nil, nil, nil)
			if result.Verdict != verdictUnsafe || !result.EvaluationFailed || strings.Contains(result.Reasoning, "secret-provider-error") {
				t.Fatalf("result = %+v", result)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := judgeEndpoint(ctx, JudgeEndpoint{Format: "openai", Endpoint: "http://127.0.0.1:1", Model: "local", MinSafeProbability: .99}, "read", nil, nil, nil)
	if !result.EvaluationFailed {
		t.Fatal("cancelled request did not fail")
	}
	result = judgeEndpoint(t.Context(), JudgeEndpoint{}, "read", nil, nil, nil)
	if !result.EvaluationFailed {
		t.Fatal("invalid config did not fail")
	}
	result = judgeEndpoint(t.Context(), JudgeEndpoint{Format: "openai", Endpoint: "http://localhost", Model: "local", MinSafeProbability: .99}, "read", nil, map[string]any{"bad": make(chan int)}, nil)
	if !result.EvaluationFailed {
		t.Fatal("unencodable request did not fail")
	}
	judge := &PermissionJudge{store: endpointStore{err: errors.New("unavailable")}}
	if !judge.JudgeWithCallback(t.Context(), "", "read", nil, nil, nil, nil).EvaluationFailed {
		t.Fatal("store failure did not fail closed")
	}
}

func TestTypeSafeProbabilityGate(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		safe, failed bool
	}{
		{"safe", `{"choice":"safe","probabilities":{"safe":0.99,"unsafe":0.005,"uncertain":0.005}}`, true, false},
		{"below threshold", `{"choice":"safe","probabilities":{"safe":0.98,"unsafe":0.01,"uncertain":0.01}}`, false, false},
		{"unsafe", `{"choice":"unsafe","probabilities":{"safe":0,"unsafe":1,"uncertain":0}}`, false, false},
		{"uncertain", `{"choice":"uncertain","probabilities":{"safe":0,"unsafe":0,"uncertain":1}}`, false, false},
		{"missing probabilities", `{"choice":"safe"}`, false, true},
		{"null probability", `{"choice":"safe","probabilities":{"safe":1,"unsafe":null,"uncertain":0}}`, false, true},
		{"unknown choice", `{"choice":"allowed","probabilities":{"safe":1,"unsafe":0,"uncertain":0}}`, false, true},
		{"wrong sum", `{"choice":"safe","probabilities":{"safe":1,"unsafe":1,"uncertain":0}}`, false, true},
		{"out of range", `{"choice":"safe","probabilities":{"safe":1.2,"unsafe":-0.2,"uncertain":0}}`, false, true},
		{"not top choice", `{"choice":"safe","probabilities":{"safe":0.1,"unsafe":0.9,"uncertain":0}}`, false, true},
		{"unknown key", `{"choice":"safe","probabilities":{"safe":1,"unsafe":0,"other":0}}`, false, true},
		{"malformed", `{`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := parseTypeSafeVerdict([]byte(`{"answers":{"verdict":`+tc.body+`}}`), .99)
			if (result.Verdict == verdictSafe) != tc.safe || result.EvaluationFailed != tc.failed {
				t.Fatalf("result = %+v", result)
			}
		})
	}
	if !parseTypeSafeVerdict([]byte(`{}`), .99).EvaluationFailed {
		t.Fatal("missing verdict accepted")
	}
}

func TestEndpointCacheScopeWithInflightSettingsChange(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing stored key")
		}
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"verdict\":\"safe\",\"reasoning\":\"Read-only.\"}"}}]}`)
	}))
	defer server.Close()
	config := JudgeEndpoint{Format: "openai", Endpoint: server.URL, Model: "first", APIKey: "test-key", MinSafeProbability: .99}
	save := func(config JudgeEndpoint) {
		t.Helper()
		encoded, _ := json.Marshal(config)
		if err := store.SetSetting(t.Context(), JudgeEndpointSettingKey, string(encoded)); err != nil {
			t.Fatal(err)
		}
	}
	save(config)
	svc := NewService(Deps{Store: store})
	svc.judge.openCodePort = func(string) string { t.Error("must use external endpoint"); return "" }
	metadata := map[string]any{"command": "git status"}
	oldHash := svc.permissionCacheHash(t.Context(), "bash", nil, metadata)
	resultCh := make(chan JudgeResult, 1)
	go func() { resultCh <- svc.judge.JudgeWithCallback(t.Context(), "", "bash", nil, metadata, nil, nil) }()
	<-started
	config.Model = "second"
	save(config)
	close(release)
	result := <-resultCh
	if result.Verdict != verdictSafe || result.EvaluationFailed {
		t.Fatalf("result = %+v", result)
	}
	resultHash := scopedPermissionHash(permissionHash("bash", nil, metadata), result.cacheScope)
	if resultHash != oldHash {
		t.Fatal("in-flight judgment lost its original scope")
	}
	svc.recordSafeCommandVerdict("session", resultHash, result.Reasoning)
	newHash := svc.permissionCacheHash(t.Context(), "bash", nil, metadata)
	if newHash == oldHash {
		t.Fatal("model change did not change cache key")
	}
	if _, ok := svc.lookupSafeCommandVerdict("session", newHash); ok {
		t.Fatal("old verdict contaminated new model's cache")
	}
	if _, ok := svc.lookupSafeCommandVerdict("session", oldHash); !ok {
		t.Fatal("old verdict was not recorded")
	}
	if scopedPermissionHash("", result.cacheScope) != "" {
		t.Fatal("empty hash became cacheable")
	}
	config.Format = ""
	save(config)
	if got := svc.permissionCacheHash(t.Context(), "bash", nil, metadata); got != permissionHash("bash", nil, metadata) {
		t.Fatal("OpenCode cache behavior changed")
	}
	if err := store.SetSetting(t.Context(), JudgeEndpointSettingKey, "{"); err != nil {
		t.Fatal(err)
	}
	if svc.permissionCacheHash(t.Context(), "bash", nil, metadata) != "" {
		t.Fatal("invalid settings allowed cache reuse")
	}
}
