//go:build codexrecord

// Command record captures app-server fixtures from a real Codex CLI. It is a
// manual tool, never run by tests: it spends model tokens.
//
// It runs `codex app-server` with an isolated CODEX_HOME (only auth.json is
// copied from the real one) in a scratch git repository, drives the turns in
// prompts, answers their approvals, then reads the thread back. Every line on the wire is written to <out>/transcript.jsonl as
// {"dir":"send"|"recv","msg":...}; the thread/read, thread/turns/list,
// thread/items/list and thread/list results are saved beside it.
//
// The codexrecord build tag keeps it out of regular builds and coverage:
//
//	go run -tags codexrecord ./internal/platforms/codex/appserver/codextest/record -out <dir>
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/gitexec"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// The first turn runs an approved command and edits a file with apply_patch;
// the second is interrupted by cancelling its command approval.
var prompts = []string{
	"Run `ls` in the current directory. Then use your apply_patch tool, not the shell, to create a file named hello.txt containing the single line `hi`. Then reply with the word done.",
	"Run the shell command `sleep 30`.",
}

func main() {
	out := flag.String("out", "", "fixture output directory")
	cli := flag.String("codex", "codex", "codex executable")
	keep := flag.String("keep", "", "keep the scratch CODEX_HOME and repository here, without auth.json")
	flag.StringVar(&scenario, "scenario", "basic", "basic: approvals and an interrupt; extras: shell, compact, review, fork, skills, account")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: record -out <dir>")
		os.Exit(2)
	}
	if err := run(*cli, *out, *keep); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type recorder struct {
	mu    sync.Mutex
	f     *os.File
	scrub func([]byte) []byte
}

func (r *recorder) line(dir string, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, _ := json.Marshal(map[string]json.RawMessage{"dir": json.RawMessage(`"` + dir + `"`), "msg": r.scrub(data)})
	_, _ = r.f.Write(append(entry, '\n'))
}

type tapWriter struct {
	w   io.WriteCloser
	rec *recorder
}

func (t tapWriter) Write(p []byte) (int, error) {
	for _, l := range splitLines(p) {
		t.rec.line("send", l)
	}
	return t.w.Write(p)
}

func (t tapWriter) Close() error { return t.w.Close() }

// scrubber removes machine and account identity from recorded bytes: the
// scratch path (and its /private form), the real home and temp directories,
// the hostname, installation, account and user ids, email addresses and the
// user agent.
func scrubber(scratch string) func([]byte) []byte {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	tmp := strings.TrimSuffix(os.TempDir(), "/")
	ids := regexp.MustCompile(`("(?:installationId|accountId|chatgptAccountId|account_id|creator_account_id|userId|user_id|creator_user_id)":\s*)"[^"]*"`)
	agent := regexp.MustCompile(`("userAgent":\s*)"[^"]*"`)
	email := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	return func(b []byte) []byte {
		b = bytes.ReplaceAll(b, []byte("/private"+scratch), []byte("/fixture"))
		b = bytes.ReplaceAll(b, []byte(scratch), []byte("/fixture"))
		for _, dir := range []string{"/private" + tmp, tmp, home} {
			if len(dir) > len("/private") {
				b = bytes.ReplaceAll(b, []byte(dir), []byte("/fixture-host"))
			}
		}
		if host != "" {
			b = bytes.ReplaceAll(b, []byte(host), []byte("host.local"))
		}
		b = ids.ReplaceAll(b, []byte(`${1}"00000000-0000-0000-0000-000000000000"`))
		b = email.ReplaceAll(b, []byte("user@example.com"))
		return agent.ReplaceAll(b, []byte(`${1}"ocman/test"`))
	}
}

func splitLines(p []byte) [][]byte {
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(p))
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			out = append(out, append([]byte(nil), sc.Bytes()...))
		}
	}
	return out
}

// scenario selects what run drives once connected.
var scenario string

func run(cli, out, keep string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	scratch, err := os.MkdirTemp(keep, "codex-record-")
	if err != nil {
		return err
	}
	if keep == "" {
		defer os.RemoveAll(scratch)
	}
	// Codex also reads the user's home (skills under ~/.agents) and TMPDIR:
	// give it scratch ones too, so nothing personal reaches the fixtures.
	home, repo := filepath.Join(scratch, "home"), filepath.Join(scratch, "repo")
	userHome, tmp := filepath.Join(scratch, "user"), filepath.Join(scratch, "tmp")
	for _, dir := range []string{home, repo, userHome, tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	realHome, _ := os.UserHomeDir()
	auth, err := os.ReadFile(filepath.Join(realHome, ".codex", "auth.json"))
	if err != nil {
		return fmt.Errorf("reading auth.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), auth, 0o600); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(home, "auth.json"))
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("scratch\n"), 0o644); err != nil {
		return err
	}
	// Not `-C repo`: gitexec would pin an enclosing checkout as the work tree.
	if b, err := gitexec.Command(context.Background(), "init", "-q", repo).CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %w: %s", err, b)
	}

	f, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	rec := &recorder{f: f, scrub: scrubber(scratch)}

	cmd := exec.Command(cli, "app-server")
	cmd.Dir, cmd.Stderr = repo, os.Stderr
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home, "HOME="+userHome, "TMPDIR="+tmp)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	client := newRPC(tapWriter{w: stdin, rec: rec})
	go client.read(stdout, rec)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := client.initialize(ctx); err != nil {
		return err
	}
	if err := client.call(ctx, codexappserver.MethodModelList, codexappserver.ModelListParams{}, nil); err != nil {
		return err
	}
	var start struct {
		Thread codexappserver.Thread `json:"thread"`
	}
	if err := client.call(ctx, "thread/start", map[string]any{"cwd": repo, "sandbox": "workspace-write", "approvalPolicy": "untrusted"}, &start); err != nil {
		return err
	}
	id := start.Thread.ID
	if scenario == "extras" {
		return extras(ctx, client, id, out, rec)
	}
	for _, text := range prompts {
		if err := client.startTurn(ctx, id, text); err != nil {
			return err
		}
		select {
		case done := <-client.turns:
			fmt.Fprintln(os.Stderr, "turn", done.ID, done.Status)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := client.call(ctx, codexappserver.MethodThreadNameSet, codexappserver.ThreadNameSetParams{ThreadID: id, Name: "Recorded fixture"}, nil); err != nil {
		return err
	}
	// Each read is saved raw, so fixtures keep every field the server sent.
	reads := []struct {
		file, method string
		params       any
	}{
		{"thread-read.json", codexappserver.MethodThreadRead, codexappserver.ThreadReadParams{ThreadID: id}},
		{"turns.json", codexappserver.MethodThreadTurns, codexappserver.ThreadTurnsListParams{ThreadID: id, SortDirection: "asc", ItemsView: "notLoaded"}},
		{"items.json", codexappserver.MethodThreadItems, codexappserver.ThreadItemsListParams{ThreadID: id, SortDirection: "asc"}},
		{"thread-list.json", codexappserver.MethodThreadList, codexappserver.ThreadListParams{SourceKinds: codexappserver.AllSourceKinds}},
	}
	for _, r := range reads {
		var raw json.RawMessage
		if err := client.call(ctx, r.method, r.params, &raw); err != nil {
			return fmt.Errorf("%s: %w", r.method, err)
		}
		if err := writeJSON(filepath.Join(out, r.file), raw, rec.scrub); err != nil {
			return err
		}
	}
	return nil
}

// writeJSON saves a raw result indented and scrubbed.
func writeJSON(path string, raw json.RawMessage, scrub func([]byte) []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return err
	}
	return os.WriteFile(path, append(scrub(pretty.Bytes()), '\n'), 0o644)
}
