package tmux

import (
	"reflect"
	"strings"
	"testing"
)

func TestManagedRunnerCommands(t *testing.T) {
	log := fakeTmux(t, `
if [ "$1" != '-L' ] || [ "$2" != 'ocman-agents' ] || [ "$3" != '-f' ] || [ "$4" != '/dev/null' ]; then exit 1; fi
shift 4
if [ "$1" = list-sessions ]; then printf 'owned\t1\t/owned/\n'; fi
`)
	ctx := t.Context()
	sessions, err := ManagedRunner.ListSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].Name != "owned" || sessions[0].Path != "/owned" {
		t.Fatalf("sessions = %v, error = %v", sessions, err)
	}
	env := map[string]string{"OPENCODE_PERMISSION": "allow"}
	if err := ManagedRunner.NewSessionEnv(ctx, "owned", "/owned", "opencode serve --port 1234", env); err != nil {
		t.Fatal(err)
	}
	if err := ManagedRunner.NewWindowEnv(ctx, "owned", "/owned", "opencode serve --port 1234", env); err != nil {
		t.Fatal(err)
	}
	if err := ManagedRunner.KillSession(ctx, "owned"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"-L ocman-agents -f /dev/null list-sessions -F",
		"-L ocman-agents -f /dev/null new-session -d -s owned -c /owned -e OPENCODE_PERMISSION=allow opencode serve --port 1234",
		"-L ocman-agents -f /dev/null new-window -d -t owned -c /owned -e OPENCODE_PERMISSION=allow opencode serve --port 1234",
		"-L ocman-agents -f /dev/null kill-session -t =owned",
	} {
		if !strings.Contains(log(), want) {
			t.Errorf("command log missing %q: %s", want, log())
		}
	}
}

func TestManagedCommandIsolation(t *testing.T) {
	t.Setenv("TMUX", "/user/tmux/socket,123,0")
	for _, operation := range []string{"list-sessions", "new-session", "new-window", "kill-session"} {
		t.Run(operation, func(t *testing.T) {
			// Inspect the executable arguments without running tmux.
			cmd, _, cancel := commandContext(t.Context(), commandTimeout, managedArgs(operation, "-t", "owned")...)
			defer cancel()
			want := []string{"tmux", "-L", "ocman-agents", "-f", "/dev/null", operation, "-t", "owned"}
			if !reflect.DeepEqual(cmd.Args, want) {
				t.Fatalf("args = %q, want %q", cmd.Args, want)
			}
		})
	}
}

// Runtimes launched before OpenCode and providers shared ocman-agents are
// still found and stopped on their old servers after an upgrade.
func TestManagedSessionLegacyServers(t *testing.T) {
	fakeTmux(t, `
case "$2:$7" in
  ocman-provider:=old|ocman-agents:=new) exit 0 ;;
  ocman-opencode:=broken) echo boom >&2; exit 2 ;;
esac
echo "can't find session" >&2; exit 1
`)
	ctx := t.Context()
	for name, want := range map[string]bool{"new": true, "old": true, "gone": false} {
		if got, err := HasManagedSession(ctx, name); err != nil || got != want {
			t.Errorf("has %s = %v, %v", name, got, err)
		}
		if err := KillManagedSession(ctx, name); (err == nil) != want {
			t.Errorf("kill %s = %v", name, err)
		}
	}
	if _, err := HasManagedSession(ctx, "broken"); err == nil {
		t.Error("a failing server must not read as a missing session")
	}
}

// tmux resolves a bare -t session target by prefix when no session has the
// exact name. Two projects whose derived names collide run as "app" and
// "app-<hash>", so a stop or liveness check for a vanished "app" must not
// reach the other project's "app-<hash>".
func TestManagedSessionTargetsAreExact(t *testing.T) {
	fakeTmux(t, `
target=$7
case "$target" in
  =*) [ "${target#=}" = 'app-0123abcd' ] && exit 0 ;;
  *) case 'app-0123abcd' in "$target"*) exit 0 ;; esac ;;
esac
echo "can't find session: $target" >&2; exit 1
`)
	ctx := t.Context()
	if alive, err := HasManagedSession(ctx, "app"); err != nil || alive {
		t.Errorf("has app = %v, %v; want the prefix match ignored", alive, err)
	}
	if err := KillManagedSession(ctx, "app"); err == nil {
		t.Error("kill app reached the session app-0123abcd")
	}
	if alive, err := HasManagedSession(ctx, "app-0123abcd"); err != nil || !alive {
		t.Errorf("has app-0123abcd = %v, %v", alive, err)
	}
	if err := KillManagedSession(ctx, "app-0123abcd"); err != nil {
		t.Errorf("kill app-0123abcd = %v", err)
	}
}
