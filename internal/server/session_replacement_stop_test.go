package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Models an adopted API server that has no managed tmux handle. Native Stop
// must fail without killing it or authorizing a duplicate replacement launch.
type untrackedTestRuntime struct {
	*ocruntime.NativeRuntime
	endpoint string
	launches int
}

func (r *untrackedTestRuntime) Launch(context.Context, ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches++
	return &ocruntime.Instance{Endpoint: r.endpoint, Kind: ocruntime.KindNativeTmux}, nil
}

func TestFailedNativeStopLeavesHistoryUnconfirmedAndServerRetained(t *testing.T) {
	t.Cleanup(ocv2.SetInstalledV2(true))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer api.Close()
	srv, reg := newSessionsTestServer(t)
	reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "active", Directory: "/repo"}}}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "turn"}, nil
	}})
	runtime := &untrackedTestRuntime{NativeRuntime: ocruntime.NewNativeRuntimeWithAuth(ocapi.Auth{}), endpoint: api.URL}
	port := ""
	host := hostlocal.New(hostlocal.Deps{Runtime: runtime, BeforeReplace: srv.recordOpencodeReplacement, BeforeStop: srv.beginOpencodeReplacementStopWithInstance, AfterStop: srv.confirmOpencodeReplacement, ReplacementStopped: srv.opencodeReplacementStopped, ReplacementStopping: srv.opencodeReplacementStopping, CancelReplacementStop: srv.cancelOpencodeReplacementStop, SetMachineServer: func(value string) { port = value }})
	if _, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	oldPort := port
	if _, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err == nil || !strings.Contains(err.Error(), "Stop requires an instance with an ID") {
		t.Fatalf("missing native Stop error: %v", err)
	}
	if runtime.launches != 1 || port != oldPort || oldPort == "" {
		t.Fatalf("failed Stop replaced live server: launches=%d port=%s old=%s", runtime.launches, port, oldPort)
	}
	notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "active")
	if err != nil || len(notices) != 0 {
		t.Fatalf("failed Stop created definitive interruption history: %v %v", notices, err)
	}
	detail := &platforms.SessionDetail{Session: &db.Session{Status: db.StatusBusy}, Messages: []db.Message{{ID: "turn", Data: json.RawMessage(`{"role":"assistant"}`)}}}
	srv.enrichSessionDetail(t.Context(), "opencode", "active", detail, true)
	if len(detail.Messages) != 1 || len(detail.Parts) != 0 {
		t.Fatal("pending preparation leaked into visible history")
	}
}
