package opencode

import (
	"context"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// withMachineServer publishes port as the v2 machine server for one test.
func withMachineServer(t *testing.T, port string) {
	t.Helper()
	SetMachineServer(port)
	t.Cleanup(func() { SetMachineServer("") })
}

func TestMachineServerPort_SetAndClear(t *testing.T) {
	if got := MachineServerPort(); got != "" {
		t.Fatalf("initial machine port = %q, want empty", got)
	}
	withMachineServer(t, "9001")
	if got := MachineServerPort(); got != "9001" {
		t.Fatalf("MachineServerPort = %q, want 9001", got)
	}
	SetMachineServer("9001") // idempotent
	if got := MachineServerPort(); got != "9001" {
		t.Fatalf("MachineServerPort after re-set = %q, want 9001", got)
	}
	SetMachineServer("")
	if got := MachineServerPort(); got != "" {
		t.Fatalf("MachineServerPort after clear = %q, want empty", got)
	}
}

func TestSetMachineServer_ResetsPortCache(t *testing.T) {
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		atomic.AddInt32(&calls, 1)
		return map[string]string{}
	})
	t.Cleanup(func() { restore(); resetPortCacheForTests() })
	resetPortCacheForTests()

	discoverOpenCodePorts()
	discoverOpenCodePorts() // cached
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
	withMachineServer(t, "9002")
	discoverOpenCodePorts()
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("discovery calls after SetMachineServer = %d, want 2 (cache reset)", got)
	}
	SetMachineServer("9002") // unchanged: must not reset
	discoverOpenCodePorts()
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("discovery calls after unchanged SetMachineServer = %d, want 2", got)
	}
}

func TestPortLookups_FallBackToMachineServer(t *testing.T) {
	root := t.TempDir()
	repo := root + "/ocman"
	worktree := root + "/.worktrees/ocman/feat-x"
	other := root + "/other"
	strayWorktree := root + "/.worktrees/unknown/feat-y"

	lookups := map[string]func(map[string]string, string) string{
		"lookupPortWithWorktreeFold": lookupPortWithWorktreeFold,
		"portForDirectory":           portForDirectory,
	}
	for name, lookup := range lookups {
		t.Run(name, func(t *testing.T) {
			ports := map[string]string{normalizePortDirectory(repo): "4096"}

			if got := lookup(ports, other); got != "" {
				t.Fatalf("v1: unknown dir = %q, want empty", got)
			}
			withMachineServer(t, "7777")
			for dir, want := range map[string]string{
				repo:          "4096", // exact still wins
				worktree:      "4096", // folded still wins
				other:         "7777",
				strayWorktree: "7777", // folds to an unserved root
			} {
				if got := lookup(ports, dir); got != want {
					t.Errorf("%s(%q) = %q, want %q", name, dir, got, want)
				}
			}
			if got := lookup(map[string]string{}, worktree); got != "7777" {
				t.Errorf("no instances = %q, want machine port", got)
			}
			SetMachineServer("")
			if got := lookup(ports, other); got != "" {
				t.Errorf("after clear = %q, want empty", got)
			}
		})
	}
}

func TestMachineServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := machineServers(); got != nil {
		t.Fatalf("machineServers without a port = %v, want nil", got)
	}
	withMachineServer(t, "7778")
	want := []openCodeServer{{directory: normalizePortDirectory(home), port: "7778"}}
	if got := machineServers(); !reflect.DeepEqual(got, want) {
		t.Fatalf("machineServers = %+v, want %+v", got, want)
	}
}

func TestDiscoverServersUncached_V2UsesMachineServer(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := discoverOpenCodeServersUncached(); len(got) != 0 {
		t.Fatalf("v2 discovery without machine server = %v, want none", got)
	}
	withMachineServer(t, "7779")
	want := []openCodeServer{{directory: normalizePortDirectory(home), port: "7779"}}
	if got := discoverOpenCodeServersUncached(); !reflect.DeepEqual(got, want) {
		t.Fatalf("v2 discovery = %+v, want %+v", got, want)
	}
}

// A session in a directory no instance was discovered for resolves to the
// machine server end to end (resolvePort → NativeQueued).
func TestResolvePort_UnknownDirectoryUsesMachineServer(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	const sid, dir = "sess-machine-port", "/tmp/proj-machine-port"
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/session/"+sid+"/inbox" {
			writeJSONBody(w, `{"data":[{"id":"msg_1","type":"user","delivery":"queue","payload":{"text":"x"}}]}`)
			return true
		}
		return false
	})
	withTestPort(t, "/somewhere/else", "1")
	withMachineServer(t, f.Port())
	a := New(newTestDBWithSession(t, sid, dir), nil)
	msgs, err := a.NativeQueued(context.Background(), sid)
	if err != nil || len(msgs) != 1 || msgs[0].ID != "msg_1" {
		t.Fatalf("NativeQueued via machine port = %+v, %v", msgs, err)
	}
}
