package ocruntime

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

func TestNativeLaunchV2(t *testing.T) {
	for _, tc := range []struct {
		name, db string
	}{{"no OPENCODE_DB", ""}, {"forwards OPENCODE_DB", "/tmp/oc-v2.db"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENCODE_DB", tc.db)
			f := &fakeLaunch{}
			rt := &NativeRuntime{launch: f.fn, auth: ocapi.New("pw")}
			if _, err := rt.Launch(t.Context(), LaunchSpec{
				RepoRoot: "/r", Port: 4242, PermissionJSON: `{"x":"allow"}`, V2: true,
			}); err != nil {
				t.Fatalf("Launch: %v", err)
			}
			if want := tmux.OpencodeServeCommandForPort(4242); f.command != want {
				t.Errorf("command = %q, want %q", f.command, want)
			}
			if _, ok := f.env["OPENCODE_PERMISSION"]; ok {
				t.Errorf("OPENCODE_PERMISSION set for v2: %v", f.env)
			}
			if f.env["OPENCODE_PASSWORD"] != "pw" {
				t.Errorf("OPENCODE_PASSWORD = %q, want pw", f.env["OPENCODE_PASSWORD"])
			}
			got, ok := f.env["OPENCODE_DB"]
			if tc.db == "" && ok {
				t.Errorf("OPENCODE_DB set to %q with empty process env", got)
			}
			if tc.db != "" && got != tc.db {
				t.Errorf("OPENCODE_DB = %q, want %q", got, tc.db)
			}
		})
	}
}

func TestNativeLaunchV1IgnoresOpencodeDB(t *testing.T) {
	t.Setenv("OPENCODE_DB", "/tmp/oc.db")
	f := &fakeLaunch{}
	rt := &NativeRuntime{launch: f.fn}
	if _, err := rt.Launch(t.Context(), LaunchSpec{RepoRoot: "/r", Port: 4242, PermissionJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if f.command != tmux.OpencodeCommandForPort(4242) {
		t.Errorf("command = %q", f.command)
	}
	if f.env["OPENCODE_PERMISSION"] != "{}" {
		t.Errorf("v1 lost OPENCODE_PERMISSION: %v", f.env)
	}
	if _, ok := f.env["OPENCODE_DB"]; ok {
		t.Errorf("v1 forwarded OPENCODE_DB: %v", f.env)
	}
}
