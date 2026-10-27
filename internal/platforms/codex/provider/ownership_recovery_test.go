package codexprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestRecoverThreadLoadedInCurrentAppServer(t *testing.T) {
	for _, status := range []string{"idle", "active", "notLoaded"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t, func(s *codextest.Server) {
				scripted(s)
				s.Handle(codexappserver.MethodThreadResume, func(json.RawMessage) (any, error) {
					return nil, &codexappserver.RPCError{Code: -32600, Message: "thread thr already has an active writer"}
				})
				s.Handle("thread/loaded/list", func(json.RawMessage) (any, error) {
					return map[string]any{"data": []string{"thr"}}, nil
				})
				s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) {
					th := thread("thr")
					th["status"] = map[string]any{"type": status}
					return map[string]any{"thread": th}, nil
				})
			})
			code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "hi"}, nil)
			want := http.StatusConflict
			if status == "idle" {
				want = http.StatusNoContent
			}
			if code != want {
				t.Fatalf("send with locally loaded %s thread = %d, want %d", status, code, want)
			}
			starts := 0
			for _, c := range h.fake().Calls() {
				if c.Method == codexappserver.MethodTurnStart {
					starts++
				}
			}
			if (starts == 1) != (status == "idle") || starts > 1 {
				t.Fatalf("%s thread started %d turns", status, starts)
			}
		})
	}
}

func TestOwnershipProbeDoesNotLaunchOrInferFromHistory(t *testing.T) {
	h := newHarness(t, scripted)
	var owned providerprotocol.SessionOwnership
	if code := h.call(http.MethodGet, "/sessions/thr/ownership", nil, &owned); code != http.StatusOK || owned.Loaded || h.fake() != nil {
		t.Fatalf("probe launched a runtime: %d %+v", code, owned)
	}
	if _, _, err := h.b.conn(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"idle", "active", "not loaded", "read error", "wrong id", "list error"} {
		t.Run(scenario, func(t *testing.T) {
			s := h.fake()
			s.Handle(codexappserver.MethodThreadLoadedList, func(json.RawMessage) (any, error) {
				if scenario == "list error" {
					return nil, errors.New("probe failed")
				}
				ids := []string{"thr"}
				if scenario == "not loaded" {
					ids = nil
				}
				return map[string]any{"data": ids}, nil
			})
			s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) {
				if scenario == "read error" {
					return nil, errors.New("read failed")
				}
				th := thread("thr")
				if scenario == "active" {
					th["status"] = map[string]any{"type": "active"}
				}
				if scenario == "wrong id" {
					th["id"] = "other"
				}
				return map[string]any{"thread": th}, nil
			})
			code := h.call(http.MethodGet, "/sessions/thr/ownership", nil, &owned)
			if scenario == "read error" || scenario == "list error" || scenario == "wrong id" {
				if code != http.StatusInternalServerError {
					t.Fatalf("failed probe = %d", code)
				}
				return
			}
			if code != http.StatusOK || owned.SourceKey != codextest.Info.CodexHome || owned.Loaded != (scenario != "not loaded") || owned.Idle != (scenario == "idle") {
				t.Fatalf("ownership = %d %+v", code, owned)
			}
		})
	}
}

func TestConcurrentLoadsResumeOnce(t *testing.T) {
	h := newHarness(t, scripted)
	c, gen, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := h.b.load(t.Context(), c, gen, "thr"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	resumes := 0
	for _, call := range h.fake().Calls() {
		if call.Method == codexappserver.MethodThreadResume {
			resumes++
		}
	}
	if resumes != 1 {
		t.Fatalf("concurrent loads resumed %d times", resumes)
	}
}
