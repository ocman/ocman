package claudeprovider

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

func firstStdin(t *testing.T, fixture string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range strings.Split(string(data), "\n") {
		var env struct {
			Direction string          `json:"direction"`
			Message   json.RawMessage `json:"message"`
		}
		if json.Unmarshal([]byte(row), &env) == nil && env.Direction == "stdin" {
			return env.Message
		}
	}
	t.Fatal("fixture has no stdin row")
	return nil
}

// The fake replays the spike's capture, and its stream-json goes through
// the same converter as stored transcripts.
func TestFakeClaudeReplaysFixture(t *testing.T) {
	binary := claudetest.Binary(t)
	if got := cliVersion(t.Context(), binary); got != claudetest.Version {
		t.Fatalf("version = %q", got)
	}
	if got := cliVersion(t.Context(), filepath.Join(t.TempDir(), "missing")); got != "unavailable" {
		t.Fatalf("missing CLI version = %q", got)
	}

	fixture := claudetest.Fixture("plain.jsonl")
	cmd := claudetest.Command(binary, fixture, "-p", "--input-format", "stream-json")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write(append(firstStdin(t, fixture), '\n')); err != nil {
		t.Fatal(err)
	}
	c := &Converter{SessionID: "live"}
	lines := bufio.NewScanner(stdout)
	for lines.Scan() {
		c.Add(lines.Bytes())
		if strings.Contains(lines.Text(), `"type":"result"`) {
			break
		}
	}
	c.Finish()
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("fake exit: %v", err)
	}
	var texts []string
	for _, p := range c.Parts {
		var part map[string]any
		_ = json.Unmarshal(p.Data, &part)
		if text, _ := part["text"].(string); text != "" {
			texts = append(texts, text)
		}
	}
	if len(c.Messages) != 2 || !strings.Contains(strings.Join(texts, "|"), "SPIKE_OK") {
		t.Fatalf("converted live stream: %d messages, texts %q", len(c.Messages), texts)
	}

	wrong := claudetest.Command(binary, fixture)
	wrong.Stdin = strings.NewReader(`{"type":"control_request"}` + "\n")
	var exit *exec.ExitError
	if err := wrong.Run(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("unexpected stdin must fail the replay, got %v", err)
	}
}

// The fake answers only what the real CLI accepts: a control_response to the
// open request id with a well-formed decision.
func TestFakeClaudeValidatesControlResponses(t *testing.T) {
	binary := claudetest.Binary(t)
	fixture := claudetest.Fixture("permission.jsonl")
	const id = "00000000-0000-4000-8000-000000000008"
	respond := func(requestID, decision string) string {
		return `{"type":"control_response","response":{"subtype":"success","request_id":"` + requestID + `","response":` + decision + `}}`
	}
	for name, tt := range map[string]struct {
		line string
		ok   bool
	}{
		"allow":            {respond(id, `{"behavior":"allow","updatedInput":{"command":"x"}}`), true},
		"deny":             {respond(id, `{"behavior":"deny","message":"no"}`), true},
		"wrong request id": {respond("00000000-0000-4000-8000-000000000999", `{"behavior":"allow","updatedInput":{}}`), false},
		"allow no input":   {respond(id, `{"behavior":"allow"}`), false},
		"deny no message":  {respond(id, `{"behavior":"deny"}`), false},
		"unknown behavior": {respond(id, `{"behavior":"maybe","updatedInput":{}}`), false},
		"error subtype":    {`{"type":"control_response","response":{"subtype":"error","request_id":"` + id + `"}}`, false},
		"malformed":        {`{"type":"control_response","response":"nope"}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := claudetest.Command(binary, fixture)
			cmd.Stdin = strings.NewReader(string(firstStdin(t, fixture)) + "\n" + tt.line + "\n")
			err := cmd.Run()
			var exit *exec.ExitError
			if tt.ok != (err == nil) || (!tt.ok && (!errors.As(err, &exit) || exit.ExitCode() != 3)) {
				t.Fatalf("ok=%v, got %v", tt.ok, err)
			}
		})
	}
}

// ocman reaches the provider only through the protocol: the generic
// adapter over the supervisor's unix socket.
func TestProtocolRoundTrip(t *testing.T) {
	config := fixture(t)
	dir, err := os.MkdirTemp("", "cp-") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	opts := Options{IndexPath: filepath.Join(dir, "claude-index.db"), ConfigDir: config, CLI: claudetest.Binary(t), Sweep: time.Hour}
	sup, err := NewSupervisor(t.Context(), opts, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")
	go func() { _ = sup.Serve(t.Context(), socket) }()
	var adapter *provider.Adapter
	for deadline := time.Now().Add(5 * time.Second); adapter == nil; {
		if adapter, err = provider.New(t.Context(), socket); err != nil && time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer adapter.Close()
	if adapter.Capabilities().AgentCatalog {
		t.Fatal("Claude must not advertise unsupported composer agent selection")
	}
	if adapter.ID() != PlatformID || adapter.Hello().CLIVersion != claudetest.Version {
		t.Fatalf("hello = %+v", adapter.Hello())
	}
	var sessions []db.Session
	// The initial sync indexes transcripts one by one: wait for the whole set.
	listed := func(id string) bool {
		return slices.ContainsFunc(sessions, func(s db.Session) bool { return s.ID == id })
	}
	for deadline := time.Now().Add(5 * time.Second); len(sessions) != 2 || !listed(mainID); time.Sleep(10 * time.Millisecond) {
		if sessions, err = adapter.Sessions(t.Context(), "", 0); err != nil || time.Now().After(deadline) {
			t.Fatalf("sessions = %+v, %v", sessions, err)
		}
	}
	detail, err := adapter.Session(t.Context(), mainID, 30, 0)
	if err != nil || len(detail.Messages) != 4 || detail.Session.Platform != PlatformID {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	if !adapter.Owns(t.Context(), childID) {
		t.Fatal("the session tree's subagent must be owned by the adapter")
	}
	life, err := adapter.SessionLifecycle(t.Context(), otherID)
	if err != nil || life.Status != db.StatusInterrupted {
		t.Fatalf("lifecycle = %+v, %v", life, err)
	}
	inactive, err := adapter.SessionsInactiveBefore(t.Context(), time.Now().UnixMilli())
	if err != nil || len(inactive) != 2 {
		t.Fatalf("auto-archive candidates = %+v, %v", inactive, err)
	}
}
