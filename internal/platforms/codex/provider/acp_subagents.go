package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/db"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type acpChild struct {
	native, parent, root string
	running              bool // observed lifecycle, including a busy event still waiting for the spool
}

// ACP v1's subagent extension uses a connection-scoped wrapper, not a native
// thread ID. Decode only this adapter's pinned format, then prove the native
// parent against the read-only app-server before publishing a child identity.
func (a *acpExecution) subagent(ctx context.Context, p acpexec.SubagentNotification) error {
	a.mu.Lock()
	parent, root := p.SessionID, p.SessionID
	if c, ok := a.children[parent]; ok {
		parent, root = c.native, c.root
	}
	knownRoot := a.loaded[root] || a.loading[root]
	child, known := a.children[p.Update.SessionID]
	a.mu.Unlock()
	if !knownRoot {
		return errors.New("codex subagent has an unknown parent")
	}
	if !known {
		prefix, encoded, ok := strings.Cut(p.Update.SessionID, ":subagent:")
		decodedRoot, rootErr := url.PathUnescape(prefix)
		id, err := url.PathUnescape(encoded)
		if !ok || rootErr != nil || err != nil || decodedRoot != root || id == "" || id == parent || id == root {
			return errors.New("invalid Codex ACP subagent identity")
		}
		native, _, err := a.b.conn(ctx)
		if err != nil {
			return err
		}
		thread, err := native.ReadThread(ctx, id, false)
		if err != nil {
			return err
		}
		if thread.ID != id || thread.ParentThreadID != parent {
			return errors.New("codex subagent native parent mismatch")
		}
		row := Session(thread)
		if p.Update.Title != nil {
			row.Title = *p.Update.Title
		}
		props, _ := json.Marshal(map[string]any{"sessionID": id, "info": map[string]any{
			"id": id, "parentID": parent, "directory": row.Directory, "title": row.Title,
			"time": map[string]any{"created": row.TimeCreated, "updated": row.TimeUpdated},
		}})
		if err := a.b.sup.Publish(providerprotocol.Event{Type: "session.created", Properties: props}, func() {
			a.b.mu.Lock()
			s := a.b.sessionLocked(id)
			s.metadata, s.dir, s.title = row, row.Directory, row.Title
			a.b.mu.Unlock()
		}); err != nil {
			return err
		}
		child = acpChild{native: id, parent: parent, root: root}
		a.mu.Lock()
		a.children[p.Update.SessionID] = child
		close(a.childChanged)
		a.childChanged = make(chan struct{})
		a.mu.Unlock()
	} else if child.parent != parent || child.root != root {
		return errors.New("codex subagent changed parent")
	}
	if p.Update.State == nil {
		return nil
	}
	status := db.StatusInterrupted
	switch p.Update.State.State {
	case "running", "requires_action":
		status = db.StatusBusy
	case "idle":
		status = db.StatusDone
		switch p.Update.State.StopReason {
		case "error":
			status = db.StatusError
		case "cancelled":
			status = db.StatusInterrupted
		}
	case "unknown": // Lost live observation is not successful completion.
	default:
		return errors.New("unknown Codex subagent state")
	}
	a.mu.Lock()
	child.running = status == db.StatusBusy
	a.children[p.Update.SessionID] = child
	a.mu.Unlock()
	a.b.emit(child.native, "", "session.status", map[string]any{"sessionID": child.native, "status": map[string]any{"type": status}})
	if status != db.StatusBusy {
		a.b.emit(child.native, "", "session.idle", map[string]any{"sessionID": child.native})
	}
	a.b.publicationMu.Lock()
	pending := len(a.b.pending[child.native]) != 0
	a.b.publicationMu.Unlock()
	if pending {
		return errors.New("codex subagent lifecycle could not be persisted")
	}
	return nil
}

// Interactions may outlive the parent's foreground turn. Resolve them through
// the negotiated child mapping, never by treating ACP wrapper IDs as native IDs.
func (a *acpExecution) interactionSession(ctx context.Context, id string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		a.mu.Lock()
		if child, ok := a.children[id]; ok {
			a.mu.Unlock()
			return child.native, true
		}
		prefix, _, wrapped := strings.Cut(id, ":subagent:")
		root, err := url.PathUnescape(prefix)
		pending := wrapped && err == nil && (a.loaded[root] || a.loading[root])
		active, changed := a.turns[id] != nil, a.childChanged
		a.mu.Unlock()
		if !pending {
			return id, active
		}
		// Requests run concurrently with ordered notifications. Wait for a
		// preceding child's proof rather than cancelling a legitimate prompt.
		select {
		case <-changed:
		case <-ctx.Done():
			return id, false
		}
	}
}

// Losing the ACP writer invalidates child observations even if the parent's
// foreground prompt already returned. Never borrow liveness from native history.
func (a *acpExecution) retireChildren(generation uint64) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.retireChildrenLocked(generation)
}

func (a *acpExecution) retireChildrenLocked(generation uint64) {
	a.mu.Lock()
	if a.generation != generation {
		a.mu.Unlock()
		return
	}
	children := make([]acpChild, 0, len(a.children))
	for id, child := range a.children {
		children = append(children, child)
		child.running = false
		a.children[id] = child
	}
	a.mu.Unlock()
	for _, child := range children {
		// Keep this generation's mapping until withdrawn child interactions are
		// durable, just as root turns do before their terminal lifecycle.
		if !a.finishInteractions(child.native) {
			return
		}
		// Queue interruption behind a failed busy write, not merely when busy
		// already reached persisted state. Otherwise recovery could revive it.
		if child.running {
			a.b.emit(child.native, "", "session.status", map[string]any{"sessionID": child.native, "status": map[string]any{"type": db.StatusInterrupted}})
			a.b.emit(child.native, "", "session.idle", map[string]any{"sessionID": child.native})
		}
	}
}
