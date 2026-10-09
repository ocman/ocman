package autoapprove

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"github.com/NoUseFreak/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
)

// Tee forwards every SSE byte unchanged and incrementally parses events.
// Parsing is lossy on malformed input, bounded in memory, and callbacks run
// outside the parser lock. Callbacks use the payload's session, never the
// session owning a directory-scoped stream. All callbacks are optional.
type Tee struct {
	W            io.Writer
	Flush        func()
	mu           sync.Mutex
	buf          []byte
	eventType    string
	dataLines    []string
	pendingBytes int
	resyncing    bool
	OnPermission func(sessionID, permissionID, permission string, patterns []string, metadata map[string]any)
	// Cancel judges and record user always/once/reject provenance.
	OnPermissionReplied func(sessionID, permissionID, reply string)
	// Directory comes from /global/event; direct /event streams leave it empty.
	OnPromptAsked      func(directory, kind string, prompt platforms.LivePrompt)
	OnPromptResolved   func(directory, kind, sessionID, requestID string)
	OnQuestionResolved func(sessionID, requestID, reason string)
	OnSessionIdle      func(sessionID string)
	// Authoritative busy/retry/idle lifecycle, not message inference.
	OnSessionStatus func(sessionID string, status SessionStatus)
	// Session updates can arrive per token; consumers must deduplicate them.
	OnSessionChanged func(sessionID string)
	OnSessionTitle   func(sessionID, title string)
	// An empty ID requires whole-list reconciliation, never guessed ownership.
	OnSessionDataChanged func(sessionID string)
	OnUserPrompt         func(sessionID string, at int64)
	OnQueueChanged       func(sessionID string)
	OnTerminalPart       func(part terminalPart)
	OnGitCommand         func(sessionID, action string)
	gitCommandHints      map[string]bool
}

type terminalPart struct {
	SessionID string
	MessageID string
	PartID    string
	CallID    string
	Command   string
	Output    string
}

// Overflow drops pending parse state and resyncs at the next event terminator.
const teeMaxPendingBytes = 4 << 20

type teeEvent struct{ typ, data string }

func (t *Tee) Write(p []byte) (int, error) {
	n, err := t.W.Write(p)
	for _, ev := range t.consume(p[:n]) {
		t.dispatchEvent(ev.typ, ev.data)
	}
	return n, err
}

// Each byte is scanned once. Partial lines and events survive Write boundaries.
func (t *Tee) consume(p []byte) []teeEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	var events []teeEvent
	for {
		idx := bytes.IndexByte(t.buf, '\n')
		if idx < 0 {
			if len(t.buf) > teeMaxPendingBytes {
				t.overflowLocked("unterminated line")
			}
			return events
		}
		line := t.buf[:idx]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		raw := string(line)
		t.buf = t.buf[idx+1:]
		switch {
		case raw == "":
			if t.resyncing {
				t.resyncing = false
			} else if len(t.dataLines) > 0 {
				events = append(events, teeEvent{t.eventType, strings.Join(t.dataLines, "\n")})
			}
			t.eventType = ""
			t.dataLines = nil
			t.pendingBytes = 0
		case t.resyncing:
		case strings.HasPrefix(raw, "event:"):
			t.eventType = strings.TrimSpace(strings.TrimPrefix(raw, "event:"))
		case strings.HasPrefix(raw, "data:"):
			d := strings.TrimSpace(strings.TrimPrefix(raw, "data:"))
			t.dataLines = append(t.dataLines, d)
			t.pendingBytes += len(d)
			if t.pendingBytes > teeMaxPendingBytes {
				t.overflowLocked("event data")
			}
		}
	}
}

func (t *Tee) overflowLocked(what string) {
	log.WithField("pending", what).Warn("autoapprove tee: pending SSE event exceeded cap; resyncing at next event boundary")
	t.buf = nil
	t.eventType = ""
	t.dataLines = nil
	t.pendingBytes = 0
	t.resyncing = true
}
