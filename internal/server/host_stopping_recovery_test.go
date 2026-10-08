package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestProductionHostRecoversStoppingCrashWithoutInventory(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			t.Cleanup(ocv2.SetInstalledV2(true))
			t.Setenv("HOME", t.TempDir())
			t.Setenv("OPENCODE_DB", "")
			opencode.SetMachineServer("")
			t.Cleanup(func() { opencode.SetMachineServer("") })
			srv, raw := testServerWithRawDB(t)
			defer raw.Close()
			path := filepath.Join(t.TempDir(), "stopping-state.db")
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
			originalHost := srv.newLocalHost()
			res, err := originalHost.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			if err != nil {
				t.Fatal(err)
			}
			original := res.Runtime
			rows, err := srv.stateDB.ManagedOpencodes(t.Context())
			if err != nil || len(rows) != 1 {
				t.Fatalf("inventory=%v err=%v", rows, err)
			}
			var root string
			for key := range rows {
				root = key
			}
			if err := srv.recordOpencodeReplacement(t.Context(), root, "original restart"); err != nil {
				t.Fatal(err)
			}
			if err := srv.beginOpencodeReplacementStopWithInstance(t.Context(), root, &original); err != nil {
				t.Fatal(err)
			}
			at, baseline, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", root)
			if err != nil || at == 0 || baseline["s"].MessageID != "user" {
				t.Fatalf("missing original snapshot: %d %v %v", at, baseline, err)
			}
			rt.stop = func() error {
				_, err := raw.Exec(`INSERT OR IGNORE INTO message(id,session_id,time_created,data) VALUES ('assistant','s',?,?)`, at, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, at))
				return err
			}
			// Shutdown succeeded, then the process crashed before AfterStop.
			if err := rt.Stop(t.Context(), &original); err != nil {
				t.Fatal(err)
			}
			if err := srv.stateDB.DeleteManagedOpencode(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			if err := srv.stateDB.Close(); err != nil {
				t.Fatal(err)
			}
			srv.stateDB, err = state.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			opencode.SetMachineServer("")
			rt.endpoint = "http://127.0.0.1:5600"
			rt.probe = func(inst *ocruntime.Instance) error {
				if inst.Endpoint == original.Endpoint {
					return ocruntime.ErrProbeUnreachable
				}
				return nil
			}
			rt.beforeLaunch = func() error {
				got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
				if err != nil {
					return err
				}
				if len(got) != 1 || got[0].MessageID != "assistant" {
					return errors.New("new launch overtook unresolved stop confirmation")
				}
				return nil
			}
			// A fresh production Host has neither an in-memory stop nor inventory.
			host := srv.newLocalHost()
			if restart {
				_, err = host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			} else {
				_, err = host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			}
			if err != nil {
				t.Fatal(err)
			}
			if rt.stops != 2 || rt.launches != 2 || rt.stopInstances[1] != original {
				t.Fatalf("recovery lost original handle: stops=%d launches=%d handles=%+v", rt.stops, rt.launches, rt.stopInstances)
			}
			got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
			if err != nil || len(got) != 1 || got[0].MessageID != "assistant" {
				t.Fatalf("original stop evidence lost: %v %v", got, err)
			}
			if pending, err := srv.opencodeReplacementStopping(t.Context(), root); err != nil || pending != nil {
				t.Fatalf("unresolved stop survived confirmation: %+v %v", pending, err)
			}
		})
	}
}
