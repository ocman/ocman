package autoapprove

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestNamedConnectionReviewPreservesRoutingIdentity(t *testing.T) {
	const connection platforms.ID = "cnx_review"
	var endpointCalls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		endpointCalls.Add(1)
		_, _ = io.WriteString(w, typeSafeTestResponse(map[string]float64{"writes": 1, "routine": 1}, nil))
	}))
	defer endpoint.Close()
	config, _ := json.Marshal(JudgeEndpoint{Format: "typesafe", Endpoint: endpoint.URL, MinSafeProbability: .99})
	store := &ruleContextStore{metricsStore: metricsStore{value: string(config)}}
	check := func(platform platforms.ID, session string) {
		t.Helper()
		if platform != connection || session != "session" {
			t.Errorf("history read routed to %q/%q, want %q/session", platform, session, connection)
		}
	}
	recentReads, freshReads, replies := 0, 0, 0
	s := NewService(Deps{
		Store: store, DefaultEnabled: true,
		SessionDir: func(string) (string, error) { return "/repo", nil },
		RecentUserMessages: func(_ context.Context, platform platforms.ID, session string) ([]string, error) {
			check(platform, session)
			recentReads++
			return []string{"Fix the project tests."}, nil
		},
		JudgeUserMessages: func(_ context.Context, platform platforms.ID, session string) (JudgeUserContext, error) {
			check(platform, session)
			freshReads++
			return JudgeUserContext{Messages: []string{"Fix the project tests."}}, nil
		},
	})
	adapter := &fakePlatform{id: "claudecode", respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		replies++
		return nil
	}}
	asked := s.rememberAskedForEnsure(string(connection), "session", "permission", "bash", nil, map[string]any{"command": "go test ./..."})
	s.backgroundAutoApprove(t.Context(), connection, adapter, "session", "permission", asked)
	if endpointCalls.Load() != 1 || freshReads != 2 || recentReads != 2 || replies != 1 {
		t.Fatalf("endpoint=%d fresh=%d recent=%d replies=%d", endpointCalls.Load(), freshReads, recentReads, replies)
	}
}
