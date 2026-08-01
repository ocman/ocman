package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeTmux installs a fake `tmux` executable at the front of PATH so the
// thin wrappers in sessions.go (which build their own exec.Command and
// therefore have no Runner seam) can be exercised without a real tmux
// server. body is an sh snippet; the returned func reads back the argv
// log so tests can assert which tmux subcommands fired.
func fakeTmux(t *testing.T, body string) func() string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$0.log\"\n" + body + "\n"
	bin := filepath.Join(dir, "tmux")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, err := os.ReadFile(bin + ".log")
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func TestListWindows(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []Window
		wantErr bool
	}{
		{
			name: "full four field line",
			body: "cat <<'EOF'\noc	/tmp/repo	opencode	exec opencode --port 0\nEOF",
			want: []Window{{Name: "oc", Path: "/tmp/repo", Command: "opencode", StartCommand: "exec opencode --port 0"}},
		},
		{
			name: "three fields leaves start command empty",
			body: "cat <<'EOF'\noc	/tmp/repo	opencode\nEOF",
			want: []Window{{Name: "oc", Path: "/tmp/repo", Command: "opencode"}},
		},
		{
			name: "two fields leaves both commands empty",
			body: "cat <<'EOF'\noc	/tmp/repo\nEOF",
			want: []Window{{Name: "oc", Path: "/tmp/repo"}},
		},
		{
			name: "path is cleaned",
			body: "cat <<'EOF'\noc	/tmp/repo/../repo/	zsh\nEOF",
			want: []Window{{Name: "oc", Path: "/tmp/repo", Command: "zsh"}},
		},
		{
			name: "malformed single field line is skipped",
			body: "cat <<'EOF'\njustaname\noc	/tmp/repo	zsh\nEOF",
			want: []Window{{Name: "oc", Path: "/tmp/repo", Command: "zsh"}},
		},
		{
			name: "blank interior line is skipped",
			body: "cat <<'EOF'\na	/tmp/a\n\nb	/tmp/b\nEOF",
			want: []Window{{Name: "a", Path: "/tmp/a"}, {Name: "b", Path: "/tmp/b"}},
		},
		{
			name: "empty output yields no windows",
			body: "exit 0",
			want: nil,
		},
		{
			name:    "tmux failure propagates",
			body:    "exit 1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readLog := fakeTmux(t, tt.body)

			got, err := ListWindows("repo")
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ListWindows() = %+v, want %+v", got, tt.want)
			}
			if log := readLog(); !strings.Contains(log, "list-windows -t repo") {
				t.Errorf("tmux argv log = %q; want a list-windows call for the session", log)
			}
		})
	}
}

func TestListSessionsCmd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		body    string
		want    []Session
		wantErr bool
	}{
		{
			name: "name and window count",
			body: "cat <<'EOF'\nrepo	3\nEOF",
			want: []Session{{Name: "repo", ResolvedPath: "/repo", Windows: 3}},
		},
		{
			name: "non digits in count are ignored",
			body: "cat <<'EOF'\nrepo	12 windows\nEOF",
			want: []Session{{Name: "repo", ResolvedPath: "/repo", Windows: 12}},
		},
		{
			name: "malformed single field line is skipped",
			body: "cat <<'EOF'\nnocount\nrepo	1\nEOF",
			want: []Session{{Name: "repo", ResolvedPath: "/repo", Windows: 1}},
		},
		{
			name: "empty output yields no sessions",
			body: "exit 0",
			want: nil,
		},
		{
			name:    "tmux failure propagates",
			body:    "exit 1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeTmux(t, tt.body)

			got, err := ListSessions()
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ListSessions() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestListClientsCmd(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []Client
		wantErr bool
	}{
		{
			name: "full four field line",
			body: "cat <<'EOF'\n/dev/ttys001	repo	120	40\nEOF",
			want: []Client{{TTY: "/dev/ttys001", Session: "repo", Width: "120", Height: "40"}},
		},
		{
			name: "short line is skipped",
			body: "cat <<'EOF'\n/dev/ttys002	repo	120\n/dev/ttys001	repo	120	40\nEOF",
			want: []Client{{TTY: "/dev/ttys001", Session: "repo", Width: "120", Height: "40"}},
		},
		{
			name: "empty output yields no clients",
			body: "exit 0",
			want: nil,
		},
		{
			name:    "tmux failure propagates",
			body:    "exit 1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeTmux(t, tt.body)

			got, err := ListClients()
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ListClients() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSwitchClient(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "success", body: "exit 0"},
		{name: "tmux failure propagates", body: "exit 1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readLog := fakeTmux(t, tt.body)

			err := SwitchClient("/dev/ttys001", "repo")
			if tt.wantErr != (err != nil) {
				t.Fatalf("SwitchClient() error = %v, wantErr %v", err, tt.wantErr)
			}
			if log := readLog(); !strings.Contains(log, "switch-client -c /dev/ttys001 -t repo") {
				t.Errorf("tmux argv log = %q; want the switch-client invocation", log)
			}
		})
	}
}

func TestLaunchOpencodeCmd(t *testing.T) {
	// list-sessions succeeds with no output so the launcher takes the
	// new-session path; new-session's exit code is per-case.
	okBody := "exit 0"
	failBody := "if [ \"$1\" = new-session ]; then exit 1; fi\nexit 0"

	tests := []struct {
		name     string
		body     string
		dirUnder string // joined onto a temp HOME
		absDir   string // used verbatim when set
		wantErr  string
		wantArg  string
	}{
		{
			name:     "creates session and runs opencode",
			body:     okBody,
			dirUnder: "src/repo",
			wantArg:  "new-session -d -s ~/src/repo",
		},
		{
			name:     "new-session failure propagates",
			body:     failBody,
			dirUnder: "src/repo",
			wantErr:  "tmux new-session",
		},
		{
			name:    "invalid derived name is rejected before tmux runs",
			body:    okBody,
			absDir:  "/var/projects/has:colon",
			wantErr: "invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			readLog := fakeTmux(t, tt.body)

			dir := tt.absDir
			if dir == "" {
				dir = filepath.Join(home, tt.dirUnder)
			}

			name, err := LaunchOpencode(dir)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v; want it to contain %q", err, tt.wantErr)
				}
				if tt.wantArg == "" && strings.Contains(tt.wantErr, "invalid characters") {
					if log := readLog(); log != "" {
						t.Errorf("tmux was invoked (%q) despite name validation failing", log)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := SessionNameForPath(dir); name != want {
				t.Errorf("name = %q, want %q", name, want)
			}
			log := readLog()
			if !strings.Contains(log, tt.wantArg) {
				t.Errorf("tmux argv log = %q; want it to contain %q", log, tt.wantArg)
			}
			if !strings.Contains(log, OpencodeCommand) {
				t.Errorf("tmux argv log = %q; want the pane command %q", log, OpencodeCommand)
			}
		})
	}
}

func TestLaunchOpencodeEnvCmd(t *testing.T) {
	// The launcher matches an existing session by resolved path, so the
	// fake reports a session name that resolves back under the temp HOME.
	existingBody := "if [ \"$1\" = list-sessions ]; then\ncat <<'EOF'\n~/src/repo	1\nEOF\nfi\nexit 0"

	tests := []struct {
		name         string
		body         string
		dir          string
		wantLaunched bool
		wantName     string
		wantErr      string
		wantEnvArg   bool
	}{
		{
			name:         "creates session with env seeded",
			body:         "exit 0",
			wantLaunched: true,
			wantName:     "~/src/repo",
			wantEnvArg:   true,
		},
		{
			name:         "existing session short circuits",
			body:         existingBody,
			wantLaunched: false,
			wantName:     "~/src/repo",
		},
		{
			name:    "new-session failure propagates",
			body:    "if [ \"$1\" = new-session ]; then exit 1; fi\nexit 0",
			wantErr: "tmux new-session",
		},
		{
			name:    "invalid derived name is rejected",
			body:    "exit 0",
			dir:     "/var/projects/has:colon",
			wantErr: "invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			readLog := fakeTmux(t, tt.body)

			dir := tt.dir
			if dir == "" {
				dir = filepath.Join(home, "src/repo")
			}

			name, launched, err := LaunchOpencodeEnv(dir, map[string]string{"OPENCODE_PERMISSION": "{}"})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v; want it to contain %q", err, tt.wantErr)
				}
				if launched {
					t.Error("launched = true on error; want false")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if launched != tt.wantLaunched {
				t.Errorf("launched = %v, want %v", launched, tt.wantLaunched)
			}
			log := readLog()
			if tt.wantEnvArg && !strings.Contains(log, "-e OPENCODE_PERMISSION={}") {
				t.Errorf("tmux argv log = %q; want the seeded -e env pair", log)
			}
			if !tt.wantLaunched && strings.Contains(log, "new-session") {
				t.Errorf("tmux argv log = %q; want no new-session on the idempotent path", log)
			}
		})
	}
}

func TestRestartOpencodeCmd(t *testing.T) {
	sessions := "if [ \"$1\" = list-sessions ]; then\ncat <<'EOF'\nrepo	2\nEOF\nfi\n"

	tests := []struct {
		name       string
		body       string
		wantTarget string
		wantErr    string
		wantErrIs  error
		wantArgs   []string
	}{
		{
			name:       "restarts the matching opencode window",
			body:       sessions + "if [ \"$1\" = list-windows ]; then\ncat <<'EOF'\nshell	/tmp/repo	zsh\noc	/tmp/repo	opencode	exec opencode --port 0\nEOF\nfi\nexit 0",
			wantTarget: "repo:oc",
			wantArgs:   []string{"kill-window -t repo:oc", "new-window -d -t repo -n oc"},
		},
		{
			name:      "no managed pane",
			body:      sessions + "if [ \"$1\" = list-windows ]; then\ncat <<'EOF'\nshell	/tmp/repo	zsh\nEOF\nfi\nexit 0",
			wantErrIs: ErrNoManagedOpencodePane,
		},
		{
			name:    "list-sessions failure propagates",
			body:    "if [ \"$1\" = list-sessions ]; then exit 1; fi\nexit 0",
			wantErr: "listing tmux sessions",
		},
		{
			name:    "list-windows failure propagates",
			body:    sessions + "if [ \"$1\" = list-windows ]; then exit 1; fi\nexit 0",
			wantErr: "listing tmux windows",
		},
		{
			name:    "kill-window failure propagates",
			body:    sessions + "if [ \"$1\" = list-windows ]; then\ncat <<'EOF'\noc	/tmp/repo	opencode\nEOF\nfi\nif [ \"$1\" = kill-window ]; then exit 1; fi\nexit 0",
			wantErr: "tmux kill-window",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readLog := fakeTmux(t, tt.body)

			target, err := RestartOpencode("/tmp/repo")
			switch {
			case tt.wantErrIs != nil:
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("error = %v, want %v", err, tt.wantErrIs)
				}
				return
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v; want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if target != tt.wantTarget {
				t.Errorf("target = %q, want %q", target, tt.wantTarget)
			}
			log := readLog()
			for _, want := range tt.wantArgs {
				if !strings.Contains(log, want) {
					t.Errorf("tmux argv log = %q; want it to contain %q", log, want)
				}
			}
		})
	}
}
