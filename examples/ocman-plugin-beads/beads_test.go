package main

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

type beadsRun struct {
	out string
	err error
}
type fakeBeadsRunner struct {
	pathErr error
	runs    []beadsRun
	seen    [][]string
	dirs    []string
	envs    [][]string
}

func (f *fakeBeadsRunner) LookPath(string) (string, error) { return "/usr/bin/bd", f.pathErr }
func (f *fakeBeadsRunner) Run(_ context.Context, _, dir string, args, env []string) ([]byte, []byte, error) {
	f.seen = append(f.seen, append([]string(nil), args...))
	f.dirs = append(f.dirs, dir)
	f.envs = append(f.envs, append([]string(nil), env...))
	run := f.runs[len(f.seen)-1]
	return []byte(run.out), nil, run.err
}
func supportedBeadsRuns(runs ...beadsRun) []beadsRun {
	return append([]beadsRun{{out: `{"version":"1.1.0"}`}}, runs...)
}
func TestSupportedBeadsVersion(t *testing.T) {
	for _, tt := range []struct {
		json string
		want bool
	}{
		{`{"version":"1.1.0"}`, true}, {`{"version":"2.0.0"}`, true},
		{`{"version":"1.0.9"}`, false}, {`{"version":"1.1"}`, false},
		{`{"version":"x.1.0"}`, false}, {`{"version":"1.x.0"}`, false},
		{`{"version":"1.1.x"}`, false}, {`{`, false},
	} {
		if got := supportedBeadsVersion([]byte(tt.json)); got != tt.want {
			t.Fatalf("%s: got %v", tt.json, got)
		}
	}
}
func TestBeadsTree(t *testing.T) {
	runner := &fakeBeadsRunner{runs: supportedBeadsRuns(
		beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`},
		beadsRun{out: `[{"id":"bd-parent","title":"Parent","status":"open","priority":1,"issue_type":"epic"},{"id":"bd-child","title":"Child","status":"in_progress","priority":2,"issue_type":"task"}]`},
		beadsRun{out: `[{"issue_id":"bd-child","depends_on_id":"bd-parent","type":"parent-child"}]`},
	)}
	got, err := readTree(t.Context(), &beadsReader{beadsRunner: runner}, plugin.PaneRead{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	want := plugin.PaneTree{Available: true, Nodes: []plugin.TreeNode{
		{ID: "bd-parent", Title: "Parent", Status: "open", Badge: "P1", Kind: "epic"},
		{ID: "bd-child", Title: "Child", Status: "in_progress", Badge: "P2", Kind: "task", ParentID: "bd-parent"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	wantCommands := [][]string{{"version", "--json"}, {"--readonly", "where", "--json"},
		{"-C", "/repo", "--readonly", "list", "--json"},
		{"-C", "/repo", "--readonly", "dep", "list", "bd-parent", "bd-child", "--type", "parent-child", "--json"}}
	if !reflect.DeepEqual(runner.seen, wantCommands) {
		t.Fatalf("commands = %v", runner.seen)
	}
	if runner.dirs[1] != "/repo" || !reflect.DeepEqual(runner.envs[1], []string{"BD_JSON_ENVELOPE=1", "BEADS_DIR=", "BEADS_DB=", "BD_DB="}) {
		t.Fatal("workspace overrides leaked")
	}
}
func TestBeadsFailureStates(t *testing.T) {
	for _, tt := range []struct {
		name string
		runs []beadsRun
		want beadsStatus
	}{
		{"unsupported version", []beadsRun{{out: `{"version":"1.0.9"}`}}, beadsStatus{}},
		{"missing workspace", supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"error":"no_beads_directory"}}`, err: errors.New("exit 1")}), beadsStatus{}},
		{"unsupported schema", supportedBeadsRuns(beadsRun{out: `{"schema_version":2,"data":{"path":"/repo/.beads"}}`}), beadsStatus{}},
		{"malformed list", supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{out: `{`}), beadsStatus{Available: true, Error: "status_unavailable"}},
		{"invalid ticket", supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{out: `[{"id":"bd-1","title":"Bad","status":"unknown","priority":1}]`}), beadsStatus{Available: true, Error: "status_unavailable"}},
		{"list failure", supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{err: errors.New("timeout")}), beadsStatus{Available: true, Error: "status_unavailable"}},
		{"dependency failure", supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":"/repo/.beads"}}`}, beadsRun{out: `[{"id":"bd-1","title":"One","status":"open","priority":1},{"id":"bd-2","title":"Two","status":"open","priority":2}]`}, beadsRun{err: errors.New("timeout")}), beadsStatus{Available: true, Tickets: []beadsTicket{{ID: "bd-1", Title: "One", Status: "open", Priority: 1}, {ID: "bd-2", Title: "Two", Status: "open", Priority: 2}}, Error: "status_unavailable"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&beadsReader{beadsRunner: &fakeBeadsRunner{runs: tt.runs}}).readBeadsStatus(t.Context(), "/repo")
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v err %v, want %#v", got, err, tt.want)
			}
		})
	}
	runner := &fakeBeadsRunner{pathErr: errors.New("missing")}
	got, err := (&beadsReader{beadsRunner: runner}).readBeadsStatus(t.Context(), "/repo")
	if err != nil || got.Available || len(runner.seen) != 0 {
		t.Fatalf("got %+v, err %v", got, err)
	}
}
func TestBeadsParsers(t *testing.T) {
	for _, data := range []string{`null`, `{}`, `[{"id":"x"}]`, `[{"id":"x","title":"t","status":"open","priority":5}]`, `[] {}`} {
		if _, ok := parseBeadsTickets([]byte(data)); ok {
			t.Fatalf("accepted %s", data)
		}
	}
	tickets := []beadsTicket{{ID: "a"}, {ID: "b"}}
	if !applyBeadsParents(tickets, []byte(`[{"issue_id":"a","depends_on_id":"b","type":"parent-child"},{"issue_id":"b","depends_on_id":"a","type":"parent-child"}]`)) || tickets[0].ParentID != "" || tickets[1].ParentID != "a" {
		t.Fatal("cycle not reduced")
	}
	if !applyBeadsParents(tickets, []byte(`[{"issue_id":"a","depends_on_id":"missing","type":"parent-child"}]`)) {
		t.Fatal("missing parent rejected")
	}
	for _, data := range []string{`null`, `{`, `[{}]`, `[{"issue_id":"a","depends_on_id":"b","type":"blocks"}]`} {
		if applyBeadsParents(tickets, []byte(data)) {
			t.Fatalf("accepted %s", data)
		}
	}
}
func TestExecBeadsRunner(t *testing.T) {
	path, err := exec.LookPath("env")
	if err != nil {
		t.Skip("env unavailable")
	}
	t.Setenv("BEADS_DIR", "/wrong/repo")
	out, _, err := (execBeadsRunner{}).Run(t.Context(), path, t.TempDir(), nil, []string{"BEADS_DIR="})
	if err != nil || strings.Count(string(out), "BEADS_DIR=") != 1 {
		t.Fatalf("env %s err %v", out, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := (execBeadsRunner{}).Run(ctx, path, t.TempDir(), nil, nil); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	buffer := limitedBuffer{remaining: 3}
	if n, err := buffer.Write([]byte("abcdef")); err != nil || n != 6 || buffer.String() != "abc" || !buffer.overflow {
		t.Fatal("unbounded output")
	}
}

type deadlineBeadsRunner struct{}

func (deadlineBeadsRunner) LookPath(string) (string, error) { return "/bd", nil }
func (deadlineBeadsRunner) Run(ctx context.Context, _, _ string, _, _ []string) ([]byte, []byte, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > beadsTimeout {
		return nil, nil, errors.New("missing deadline")
	}
	return nil, nil, context.DeadlineExceeded
}
func TestBeadsDeadline(t *testing.T) {
	_, err := (&beadsReader{beadsRunner: deadlineBeadsRunner{}}).readBeadsStatus(t.Context(), "/repo")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestMalformedRefreshKeepsWorkspaceAvailable(t *testing.T) {
	rows := `[{"id":"a","title":"Parent","status":"open","priority":1},{"id":"b","title":"Child","status":"open","priority":2}]`
	for _, malformed := range []string{"list", "dependencies"} {
		t.Run(malformed, func(t *testing.T) {
			refreshRows, refreshDeps := rows, `{`
			if malformed == "list" {
				refreshRows = `{`
			}
			runs := supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":".beads"}}`}, beadsRun{out: rows}, beadsRun{out: `[{"issue_id":"b","depends_on_id":"a","type":"parent-child"}]`})
			runs = append(runs, supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":".beads"}}`}, beadsRun{out: refreshRows}, beadsRun{out: refreshDeps})...)
			reader := &beadsReader{beadsRunner: &fakeBeadsRunner{runs: runs}}
			handler := plugin.PaneHandler(description(), func(ctx context.Context, r plugin.PaneRead) (plugin.PaneTree, error) { return readTree(ctx, reader, r) })
			call := plugin.Call{Capability: "pane", Version: plugin.PaneCapability.Version, Method: "read", Params: json.RawMessage(`{"paneId":"tickets","directory":"/repo"}`)}
			first, err := handler(t.Context(), call, nil)
			if err != nil || !strings.Contains(string(first), `"parentId":"a"`) {
				t.Fatalf("initial read %s: %v", first, err)
			}
			data, err := handler(t.Context(), call, nil)
			var tree plugin.PaneTree
			if err != nil || json.Unmarshal(data, &tree) != nil || !tree.Available || !tree.Warning {
				t.Fatalf("refresh %s: %v", data, err)
			}
		})
	}
}

func TestPaneHandlerNormalizesDisplayText(t *testing.T) {
	for _, title := range []string{"First\nsecond", "First\x00second", strings.Repeat("x", 5000), "a" + strings.Repeat("😀", 1100), "\n\t\x00"} {
		t.Run(title[:min(len(title), 20)], func(t *testing.T) {
			row, _ := json.Marshal([]map[string]any{{"id": "a", "title": title, "status": "open", "priority": 1, "issue_type": "task\n" + strings.Repeat("x", 150)}})
			reader := &beadsReader{beadsRunner: &fakeBeadsRunner{runs: supportedBeadsRuns(beadsRun{out: `{"schema_version":1,"data":{"path":".beads"}}`}, beadsRun{out: string(row)})}}
			handler := plugin.PaneHandler(description(), func(ctx context.Context, r plugin.PaneRead) (plugin.PaneTree, error) { return readTree(ctx, reader, r) })
			data, err := handler(t.Context(), plugin.Call{Capability: "pane", Version: plugin.PaneCapability.Version, Method: "read", Params: json.RawMessage(`{"paneId":"tickets","directory":"/repo"}`)}, nil)
			var tree plugin.PaneTree
			if err != nil || json.Unmarshal(data, &tree) != nil || len(tree.Nodes) != 1 {
				t.Fatalf("%s: %v", data, err)
			}
			node := tree.Nodes[0]
			if node.Title == "" || len(node.Title) > 4096 || len(node.Kind) > 128 || !utf8.ValidString(node.Title) || strings.ContainsFunc(node.Title, unicode.IsControl) || strings.ContainsFunc(node.Kind, unicode.IsControl) {
				t.Fatalf("invalid display node: %+v", node)
			}
			if title == "First\nsecond" && node.Title != "First second" {
				t.Fatal(node.Title)
			}
		})
	}
}
