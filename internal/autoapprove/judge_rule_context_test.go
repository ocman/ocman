package autoapprove

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

type ruleContextStore struct {
	metricsStore
	sections   []state.PromptSection
	sectionErr error
	approved   []state.ApprovedPermission
}

func (s *ruleContextStore) RecordApprovedPermission(_ context.Context, _, _ string, row state.ApprovedPermission) error {
	s.approved = append(s.approved, row)
	return nil
}

func (s *ruleContextStore) GetPromptSections(context.Context) ([]state.PromptSection, error) {
	return s.sections, s.sectionErr
}

func (s *ruleContextStore) GetSetting(ctx context.Context, key string) (string, bool, error) {
	if key != JudgeEndpointSettingKey {
		return "", false, nil
	}
	return s.metricsStore.GetSetting(ctx, key)
}

func TestTypeSafeContextAndPolicyChangesInvalidateInflightReview(t *testing.T) {
	config := JudgeEndpoint{Format: "typesafe", Endpoint: "https://example.com", MinSafeProbability: .99}
	encoded, _ := json.Marshal(config)
	store := &ruleContextStore{metricsStore: metricsStore{value: string(encoded)}}
	full := []string{"Do not push.", strings.Repeat("x", 500) + " Do not delete."}
	var historyErr error
	s := NewService(Deps{Store: store,
		RecentUserMessages: func(context.Context, platforms.ID, string) ([]string, error) { return []string{"recent"}, nil },
		JudgeUserMessages:  func(context.Context, platforms.ID, string) ([]string, error) { return full, historyErr },
	})
	p := &fakePlatform{id: "test"}
	sections := s.resolveJudgeSections(t.Context(), p, "/repo", "session")
	payload, _, available := typeSafePayload("/repo", "bash", nil, nil, sections)
	if !available || !reflect.DeepEqual(payload["state"].(map[string]any)["user_messages"], full) {
		t.Fatalf("context = %+v", payload)
	}
	result := parseTypeSafeRules([]byte(typeSafeTestResponse(nil, nil)), typeSafeRules(nil), .99, true)
	result.cacheScope = endpointCacheScope(config)
	if !s.ruleReviewCurrent(t.Context(), p, "/repo", "session", sections, result) {
		t.Fatal("unchanged context invalidated")
	}
	full = append(full, "Stop; do not run anything.")
	if s.ruleReviewCurrent(t.Context(), p, "/repo", "session", sections, result) {
		t.Fatal("new restriction did not invalidate approval")
	}
	full = full[:2]
	store.sections = []state.PromptSection{{Title: "Policy", Content: "No writes"}}
	if s.ruleReviewCurrent(t.Context(), p, "/repo", "session", sections, result) {
		t.Fatal("policy change did not invalidate approval")
	}
	store.sections = nil
	store.value = `{"format":"typesafe","endpoint":"https://example.com","minSafeProbability":0.98}`
	if s.ruleReviewCurrent(t.Context(), p, "/repo", "session", sections, result) {
		t.Fatal("endpoint change did not invalidate approval")
	}
	store.value = "{"
	if s.ruleReviewCurrent(t.Context(), p, "/repo", "session", sections, result) {
		t.Fatal("broken endpoint settings accepted")
	}
	store.value = string(encoded)
	historyErr = errors.New("incomplete")
	sections = s.resolveJudgeSections(t.Context(), p, "/repo", "session")
	if _, _, available = typeSafePayload("/repo", "bash", nil, nil, sections); available {
		t.Fatal("partial intent accepted after history error")
	}
	historyErr = nil
	store.sectionErr = errors.New("policy unavailable")
	sections = s.resolveJudgeSections(t.Context(), p, "/repo", "session")
	if _, _, available = typeSafePayload("/repo", "bash", nil, nil, sections); available {
		t.Fatal("missing policy accepted")
	}
	if !s.ruleReviewCurrent(t.Context(), p, "/repo", "session", nil, JudgeResult{Verdict: verdictSafe}) {
		t.Fatal("non-Jev reviewer behavior changed")
	}
}

func TestTypeSafeFullContextWithoutRecentExcerpts(t *testing.T) {
	store := &metricsStore{value: `{"format":"typesafe","endpoint":"https://example.com","minSafeProbability":0.99}`}
	s := NewService(Deps{Store: store, JudgeUserMessages: func(context.Context, platforms.ID, string) ([]string, error) { return []string{"Do not deploy."}, nil }})
	sections := s.resolveJudgeSections(t.Context(), &fakePlatform{id: "test"}, "/repo", "session")
	if len(sections) != 1 || sections[0].Content != "" || !sections[0].userIntent {
		t.Fatalf("%+v", sections)
	}
	if _, _, available := typeSafePayload("/repo", "read", nil, nil, sections); !available {
		t.Fatal("full intent unavailable")
	}
	if got := judgePrompt("read", nil, nil, sections); strings.Contains(got, "Do not deploy") {
		t.Fatal("internal evidence leaked into other reviewer prompts")
	}
}

func TestTypeSafePipelineRechecksContextBeforeReply(t *testing.T) {
	for _, scenario := range []string{"unchanged", "new user restriction", "refresh failed"} {
		t.Run(scenario, func(t *testing.T) {
			changed := scenario != "unchanged"
			var evaluated atomic.Bool
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				evaluated.Store(true)
				_, _ = io.WriteString(w, typeSafeTestResponse(map[string]float64{"writes": 1, "routine": 1}, nil))
			}))
			defer server.Close()
			config, _ := json.Marshal(JudgeEndpoint{Format: "typesafe", Endpoint: server.URL, MinSafeProbability: .99})
			store := &ruleContextStore{metricsStore: metricsStore{value: string(config)}}
			replies := 0
			p := &fakePlatform{id: "test", respondPermissionFn: func(platforms.RespondPermissionRequest) error { replies++; return nil }}
			read := func(context.Context, platforms.ID, string) ([]string, error) {
				if scenario == "new user restriction" && evaluated.Load() {
					return []string{"Stop; do not change files."}, nil
				}
				return []string{"Fix the project tests."}, nil
			}
			freshReads := 0
			s := NewService(Deps{Store: store, DefaultEnabled: true, SessionDir: func(string) (string, error) { return "/repo", nil }, RecentUserMessages: read,
				JudgeUserMessages: func(ctx context.Context, platform platforms.ID, id string) ([]string, error) {
					freshReads++
					if scenario == "refresh failed" && evaluated.Load() {
						return nil, errors.New("fresh history unavailable")
					}
					return read(ctx, platform, id)
				},
			})
			metadata := map[string]any{"command": "go test ./..."}
			// Even a previously safe command must reach Jev, not its old cache.
			s.recordSafeCommandVerdict("session", s.scopedTestHash(t, metadata), "old consent")
			asked := s.rememberAskedForEnsure("test", "session", "permission", "bash", nil, metadata)
			s.backgroundAutoApprove(t.Context(), "test", p, "session", "permission", asked)
			if calls.Load() != 1 || freshReads != 2 || replies != map[bool]int{false: 1, true: 0}[changed] {
				t.Fatalf("calls=%d freshReads=%d replies=%d", calls.Load(), freshReads, replies)
			}
			if !changed && (len(store.approved) != 1 || !store.approved[0].ContextBound) {
				t.Fatalf("approval can be inherited: %+v", store.approved)
			}
			if changed {
				status, ok := s.lookupAutoApproveStatus("session", "permission")
				if !ok || status.verdict != verdictUnsafe || !strings.Contains(status.reasoning, "context changed") {
					t.Fatalf("status=%+v", status)
				}
			}
		})
	}
}

func (s *Service) scopedTestHash(t *testing.T, metadata map[string]any) string {
	t.Helper()
	config, err := LoadJudgeEndpoint(t.Context(), s.deps.Store)
	if err != nil {
		t.Fatal(err)
	}
	return scopedPermissionHash(permissionHash("bash", nil, metadata), endpointCacheScope(config))
}
