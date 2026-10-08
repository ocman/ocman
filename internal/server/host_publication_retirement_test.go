package server

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestProductionPublicationRetiresRuntimeBeforeColdRecovery(t *testing.T) {
	for _, deletionFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "crash-after-publication", true: "post-publication-delete-failure"}[deletionFailure], func(t *testing.T) {
			t.Cleanup(ocv2.SetInstalledV2(true))
			t.Setenv("HOME", t.TempDir())
			t.Setenv("OPENCODE_DB", "")
			opencode.SetMachineServer("")
			t.Cleanup(func() { opencode.SetMachineServer("") })
			srv, raw := testServerWithRawDB(t)
			defer raw.Close()
			path := filepath.Join(t.TempDir(), "retirement-state.db")
			durable, err := state.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			srv.stateDB = durable
			defer func() { _ = srv.stateDB.Close() }()
			if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('user','s',1,'{"role":"user"}')`); err != nil {
				t.Fatal(err)
			}
			srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
				return &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "user", LatestMessageCreated: 1}, nil
			}})
			rt := &recoveryWiringRuntime{}
			srv.runtime = rt
			host := srv.newLocalHost()
			res, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			if err != nil {
				t.Fatal(err)
			}
			original := res.Runtime
			rows, err := srv.stateDB.ManagedOpencodes(t.Context())
			if err != nil || len(rows) != 1 {
				t.Fatalf("missing genuine managed row: %v %v", rows, err)
			}
			var root string
			for key := range rows {
				root = key
			}
			rt.stop = func() error {
				at, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", root)
				if err != nil {
					return err
				}
				_, err = raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('assistant','s',?,?)`, at, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, at))
				return err
			}
			rt.endpoint = "http://127.0.0.1:5600"
			if deletionFailure {
				// Fail the separate host cleanup, not deletion inside publication.
				storage, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = storage.Exec(`CREATE TRIGGER reject_late_runtime_delete BEFORE DELETE ON managed_opencode
					WHEN (SELECT phase FROM session_replacement WHERE platform='opencode' AND replacement_root=OLD.repo_root)='confirmed'
					BEGIN SELECT RAISE(ABORT,'post-publication deletion failed'); END`)
				_ = storage.Close()
				if err != nil {
					t.Fatal(err)
				}
				// The unpatched path errors here. Continue into cold recovery to
				// prove whether that error left a reusable stopped row behind.
				_, err = host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
				if err != nil && !strings.Contains(err.Error(), "post-publication deletion failed") {
					t.Fatal(err)
				}
			} else {
				if err := srv.recordOpencodeReplacement(t.Context(), root, "restart"); err != nil {
					t.Fatal(err)
				}
				if err := srv.beginOpencodeReplacementStopWithInstance(t.Context(), root, &original); err != nil {
					t.Fatal(err)
				}
				if err := rt.Stop(t.Context(), &original); err != nil {
					t.Fatal(err)
				}
				if err := srv.confirmOpencodeReplacement(t.Context(), root); err != nil {
					t.Fatal(err)
				}
				// Crash before the host gets to its independent inventory Delete.
			}
			if notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s"); err != nil || len(notices) != 1 {
				t.Fatalf("publication did not complete: %v %v", notices, err)
			}
			if err := srv.stateDB.Close(); err != nil {
				t.Fatal(err)
			}
			srv.stateDB, err = state.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			opencode.SetMachineServer("")
			oldProbes := 0
			rt.probe = func(inst *ocruntime.Instance) error {
				if inst.Endpoint == original.Endpoint {
					oldProbes++
				}
				return nil
			}
			cold := srv.newLocalHost()
			res, err = cold.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if oldProbes != 0 || res.Endpoint == original.Endpoint || rt.launches != 2 {
				t.Fatalf("cold owner reused retired runtime: old probes=%d endpoint=%s launches=%d", oldProbes, res.Endpoint, rt.launches)
			}
		})
	}
}

func TestProductionRestartDrainsHistoricalSessionsInFirstRequest(t *testing.T) {
	for _, count := range []int{1024, 1025} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			t.Cleanup(ocv2.SetInstalledV2(true))
			t.Setenv("HOME", t.TempDir())
			t.Setenv("OPENCODE_DB", "")
			opencode.SetMachineServer("")
			t.Cleanup(func() { opencode.SetMachineServer("") })
			srv, reg := newSessionsTestServer(t)
			sessions := make([]db.Session, count)
			for i := range sessions {
				sessions[i] = db.Session{ID: fmt.Sprintf("s%04d", i), Directory: "/repo"}
			}
			reg.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: sessions}, lifecycle: func(id string) (*platforms.SessionLifecycle, error) {
				return &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: "old-" + id}, nil
			}})
			rt := &recoveryWiringRuntime{stop: func() error { return nil }}
			srv.runtime = rt
			host := srv.newLocalHost()
			if _, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
				t.Fatal(err)
			}
			if _, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
				t.Fatalf("first restart required another request: %v", err)
			}
			if rt.launches != 2 || rt.stops != 1 {
				t.Fatalf("restart did not launch once: launches=%d stops=%d", rt.launches, rt.stops)
			}
			if notices, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", sessions[0].ID); err != nil || len(notices) != 0 {
				t.Fatalf("settled history gained notices: %v %v", notices, err)
			}
		})
	}
}
