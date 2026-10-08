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
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type replacementReviewRuntime struct {
	fakeRuntime
	stop func() error
}

func (r *replacementReviewRuntime) Stop(context.Context, *ocruntime.Instance) error { return r.stop() }

func replacementReviewHost(t *testing.T, srv *Server, rt *replacementReviewRuntime) *hostlocal.Host {
	t.Helper()
	t.Cleanup(ocv2.SetInstalledV2(true))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	return hostlocal.New(hostlocal.Deps{Runtime: rt, ManagedStore: managedStoreOrNil(srv.stateDB), BeforeReplace: srv.recordOpencodeReplacement, BeforeStop: srv.beginOpencodeReplacementStopWithInstance, AfterStop: srv.confirmOpencodeReplacement, ReplacementStopped: srv.opencodeReplacementStopped, ReplacementStopping: srv.opencodeReplacementStopping, CancelReplacementStop: srv.cancelOpencodeReplacementStop})
}

func TestReplacementReviewSettledBaseline(t *testing.T) {
	for _, status := range []db.SessionStatus{db.StatusDone, db.StatusWaiting} {
		t.Run(string(status), func(t *testing.T) {
			srv, raw := newInterruptionRawServer(t)
			defer raw.Close()
			_, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo');
				INSERT INTO message(id,session_id,data) VALUES ('m','s','{"role":"assistant"}');
				INSERT INTO part(id,message_id,session_id,data) VALUES ('p','m','s','{"type":"step-start"}')`)
			if err != nil {
				t.Fatal(err)
			}
			srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
				return &platforms.SessionLifecycle{Status: status, LatestMessageID: "m"}, nil
			}})
			if err := recordStoppedReplacement(t, srv, "/repo", "requested restart"); err != nil {
				t.Fatal(err)
			}
			got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
			if err != nil || len(got) != 0 {
				t.Fatalf("unchanged settled history got notice: %v %v", got, err)
			}
			if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "next restart"); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('new','s',200,'{"role":"user"}')`); err != nil {
				t.Fatal(err)
			}
			if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
				t.Fatal(err)
			}
			got, err = srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
			if err != nil || len(got) != 1 || got[0].MessageID != "new" {
				t.Fatalf("settled baseline hid a new turn: %v %v", got, err)
			}
		})
	}
}

func TestReplacementReviewFailedStopRefreshesTurn(t *testing.T) {
	srv, raw := testServerWithRawDB(t)
	defer raw.Close()
	_, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo');
		INSERT INTO message(id,session_id,time_created,data) VALUES ('old','s',1,'{"role":"assistant"}');
		INSERT INTO part(id,message_id,session_id,data) VALUES ('p','old','s','{"type":"step-start"}')`)
	if err != nil {
		t.Fatal(err)
	}
	status, message := db.StatusBusy, "old"
	srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
		return &platforms.SessionLifecycle{Status: status, LatestMessageID: message}, nil
	}})
	stopErr := errors.New("stop failed")
	rt := &replacementReviewRuntime{stop: func() error { return stopErr }}
	host := replacementReviewHost(t, srv, rt)
	if _, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, stopErr) {
		t.Fatalf("expected failed Stop: %v", err)
	}
	// Stop failed: no AfterStop callback. The old turn then finishes and the
	// user manually aborts a different follow-up before trying replacement again.
	_, err = raw.Exec(fmt.Sprintf(`UPDATE message SET data='{"role":"assistant","finish":"stop"}' WHERE id='old';
		INSERT INTO message(id,session_id,time_created,data) VALUES ('followup','s',2,'{"role":"assistant","error":{"name":"MessageAbortedError"},"time":{"completed":%d}}')`, time.Now().UnixMilli()+1))
	if err != nil {
		t.Fatal(err)
	}
	status, message = db.StatusWaiting, "followup"
	rt.stop = func() error { return nil }
	if _, err := host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 0 {
		t.Fatalf("failed attempt attributed manual abort to replacement: %v %v", got, err)
	}
}

func TestReplacementReviewAbortDuringStopAndRecovery(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovery=%t", recovery), func(t *testing.T) {
			srv, raw := testServerWithRawDB(t)
			defer raw.Close()
			path := filepath.Join(t.TempDir(), "replacement-state.db")
			durable, err := state.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.stateDB.Close() }()
			srv.stateDB = durable
			_, err = raw.Exec(`INSERT INTO session(id,directory) VALUES ('s','/repo');
				INSERT INTO message(id,session_id,data) VALUES ('m','s','{"role":"assistant"}');
				INSERT INTO part(id,message_id,session_id,data) VALUES ('p','m','s','{"type":"step-start"}')`)
			if err != nil {
				t.Fatal(err)
			}
			status := db.StatusBusy
			srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(string) (*platforms.SessionLifecycle, error) {
				return &platforms.SessionLifecycle{Status: status, LatestMessageID: "m"}, nil
			}})
			aborted := ""
			rt := &replacementReviewRuntime{stop: func() error {
				status = db.StatusWaiting
				aborted = fmt.Sprintf(`{"role":"assistant","error":{"name":"MessageAbortedError"},"time":{"completed":%d}}`, time.Now().UnixMilli())
				data := aborted
				if recovery {
					data = "invalid JSON"
				} // Force final persisted lifecycle reconciliation to fail.
				_, err := raw.Exec(`UPDATE message SET data=? WHERE id='m'`, data)
				return err
			}}
			host := replacementReviewHost(t, srv, rt)
			if _, err := host.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
				t.Fatal(err)
			}
			_, err = host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			if recovery {
				if err == nil {
					t.Fatal("invalid final lifecycle did not fail confirmation")
				}
				got, readErr := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
				if readErr != nil || len(got) != 0 {
					t.Fatalf("partial confirmation: %v %v", got, readErr)
				}
				if err := srv.stateDB.Close(); err != nil {
					t.Fatal(err)
				}
				srv.stateDB, err = state.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := raw.Exec(`UPDATE message SET data=? WHERE id='m'`, aborted); err != nil {
					t.Fatal(err)
				}
				rt.stop = func() error { return nil }
				// Rebuild the host from the durable managed handle, as on restart.
				host = hostlocal.New(hostlocal.Deps{Runtime: rt, ManagedStore: managedStoreOrNil(srv.stateDB), BeforeReplace: srv.recordOpencodeReplacement, BeforeStop: srv.beginOpencodeReplacementStopWithInstance, AfterStop: srv.confirmOpencodeReplacement, ReplacementStopped: srv.opencodeReplacementStopped, ReplacementStopping: srv.opencodeReplacementStopping, CancelReplacementStop: srv.cancelOpencodeReplacementStop})
				_, err = host.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", "s")
			if err != nil || len(got) != 1 || got[0].MessageID != "m" {
				t.Fatalf("stop abort lost its original turn: %v %v", got, err)
			}
		})
	}
}
