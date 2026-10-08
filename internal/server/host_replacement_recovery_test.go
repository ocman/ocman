package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

type recoveryWiringRuntime struct {
	fakeRuntime
	launches, stops, probes int
	stop                    func() error
	beforeLaunch            func() error
	probe                   func(*ocruntime.Instance) error
	stopInstances           []ocruntime.Instance
}

func TestOpencodeReplacementStoppedState(t *testing.T) {
	if stopped, err := (&Server{}).opencodeReplacementStopped(t.Context(), "/root"); err != nil || stopped {
		t.Fatalf("nil state: %t %v", stopped, err)
	}
	srv, _ := newSessionsTestServer(t)
	if stopped, err := srv.opencodeReplacementStopped(t.Context(), "/root"); err != nil || stopped {
		t.Fatalf("missing phase: %t %v", stopped, err)
	}
	if err := srv.stateDB.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if stopped, err := srv.opencodeReplacementStopped(t.Context(), "/root"); err != nil || stopped {
		t.Fatalf("preparation claimed closure: %t %v", stopped, err)
	}
	if err := srv.stateDB.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if stopped, err := srv.opencodeReplacementStopped(t.Context(), "/root"); err != nil || !stopped {
		t.Fatalf("closure not exposed: %t %v", stopped, err)
	}
	if err := srv.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.opencodeReplacementStopped(t.Context(), "/root"); err == nil {
		t.Fatal("state lookup failure was hidden")
	}
}

func (r *recoveryWiringRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches++
	if r.beforeLaunch != nil {
		if err := r.beforeLaunch(); err != nil {
			return nil, err
		}
	}
	return r.fakeRuntime.Launch(ctx, spec)
}

func (r *recoveryWiringRuntime) Probe(_ context.Context, inst *ocruntime.Instance) error {
	r.probes++
	if r.probe != nil {
		return r.probe(inst)
	}
	return nil
}
func (r *recoveryWiringRuntime) Stop(_ context.Context, inst *ocruntime.Instance) error {
	r.stops++
	r.stopInstances = append(r.stopInstances, *inst)
	return r.stop()
}

func TestProductionHostRecoversStoppedReplacementWithoutRuntimeRow(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			t.Cleanup(ocv2.SetInstalledV2(true))
			t.Setenv("HOME", t.TempDir())
			t.Setenv("OPENCODE_DB", "")
			opencode.SetMachineServer("")
			t.Cleanup(func() { opencode.SetMachineServer("") })
			srv, raw := testServerWithRawDB(t)
			defer raw.Close()
			path := filepath.Join(t.TempDir(), "recovery-state.db")
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
			rt := &recoveryWiringRuntime{stop: func() error {
				_, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('assistant','s',?,'invalid JSON')`, time.Now().UnixMilli())
				return err
			}}
			srv.runtime = rt
			host := srv.newLocalHost()
			if _, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
				t.Fatal(err)
			}
			rows, err := srv.stateDB.ManagedOpencodes(t.Context())
			if err != nil || len(rows) != 1 {
				t.Fatalf("managed rows=%v err=%v", rows, err)
			}
			var root string
			for key := range rows {
				root = key
			}
			if _, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err == nil {
				t.Fatal("invalid final evidence did not fail confirmation")
			}
			stopped, err := srv.stateDB.ReplacementStopped(t.Context(), "opencode", root)
			if err != nil || !stopped {
				t.Fatalf("missing durable stop proof: %t %v", stopped, err)
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
			// No in-memory stopped flag and no runtime row: only state proves closure.
			host = srv.newLocalHost()
			rt.beforeLaunch = func() error {
				got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
				if err != nil {
					return err
				}
				if len(got) != 1 {
					return errors.New("launch overtook interruption confirmation")
				}
				return nil
			}
			run := func() error {
				if restart {
					_, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
					return err
				}
				_, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
				return err
			}
			probes := rt.probes
			if err := run(); err == nil {
				t.Fatal("failed confirmation was bypassed")
			}
			if rt.launches != 1 || rt.stops != 1 || rt.probes != probes {
				t.Fatalf("recovery touched runtime before confirming: launches=%d stops=%d probes=%d", rt.launches, rt.stops, rt.probes)
			}
			at, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", root)
			if err != nil || at == 0 {
				t.Fatalf("stop boundary lost: %d %v", at, err)
			}
			if _, err := raw.Exec(`UPDATE message SET data=? WHERE id='assistant'`, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, at)); err != nil {
				t.Fatal(err)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
			if err != nil || len(got) != 1 || got[0].MessageID != "assistant" || rt.launches != 2 || rt.stops != 1 {
				t.Fatalf("recovered history=%v err=%v launches=%d stops=%d", got, err, rt.launches, rt.stops)
			}
		})
	}
}
