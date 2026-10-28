package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// HistoryClient is the subset of the app-server client history reads use.
// Stored threads are readable without loading or resuming them, so these
// calls never start a turn.
type HistoryClient interface {
	ListThreads(context.Context, codexappserver.ThreadListParams) ([]codexappserver.Thread, error)
	ReadThread(ctx context.Context, id string, turns bool) (*codexappserver.Thread, error)
	ListTurns(ctx context.Context, threadID, itemsView string) ([]codexappserver.Turn, error)
	ListItems(ctx context.Context, threadID string) ([]codexappserver.ItemEntry, error)
}

// HistoryReader serves Codex threads to history ingestion.
type HistoryReader struct {
	Client HistoryClient
}

// Inventory lists every stored thread, archived ones and subagent threads
// included. A failed listing is an error, never an empty inventory.
func (h *HistoryReader) Inventory(ctx context.Context) ([]db.HistoryEntry, error) {
	var out []db.HistoryEntry
	seen := map[string]bool{}
	for _, archived := range []bool{false, true} {
		threads, err := h.Client.ListThreads(ctx, codexappserver.ThreadListParams{SourceKinds: codexappserver.AllSourceKinds,
			Archived: &archived, SortKey: "updated_at", SortDirection: "desc"})
		if err != nil {
			return nil, fmt.Errorf("listing codex threads: %w", err)
		}
		for i := range threads {
			if t := &threads[i]; !t.Ephemeral && !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, db.HistoryEntry{ID: t.ID, Version: threadVersion(t)})
			}
		}
	}
	return out, nil
}

// Snapshot reads one thread's complete history. The thread's version is
// sampled before and after its turns and items are read; a change in between
// fails with db.ErrHistoryChanged so ingestion retries rather than storing a
// mix.
func (h *HistoryReader) Snapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	before, err := h.read(ctx, id)
	if err != nil {
		return nil, err
	}
	version := threadVersion(before)
	turns, err := h.Client.ListTurns(ctx, id, "notLoaded")
	if err != nil {
		return nil, notFound(err)
	}
	items, err := h.Client.ListItems(ctx, id)
	if err != nil {
		return nil, notFound(err)
	}
	after, err := h.read(ctx, id)
	if err != nil {
		return nil, err
	}
	if threadVersion(after) != version {
		return nil, db.ErrHistoryChanged
	}
	c := &Converter{Messages: []db.Message{}, Parts: []db.Part{}}
	if after.Path != "" {
		// Usage is best effort: history without it is still history.
		if usage, err := ReadRolloutUsage(after.Path); err == nil {
			c.Usage = usage
		}
	}
	c.AddHistory(after, turns, items)
	return &db.HistorySnapshot{Session: Session(after), InferredStatus: inferStatus(c.Messages), Messages: c.Messages,
		Parts: c.Parts, Version: version, Subagent: after.ParentThreadID != "" || after.Source.Kind == "subAgent"}, nil
}

// SessionIdentityMetadata reads only thread metadata, never content.
func (h *HistoryReader) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	t, err := h.read(ctx, id)
	if err != nil {
		return nil, err
	}
	s := Session(t)
	return &s, nil
}

func (h *HistoryReader) read(ctx context.Context, id string) (*codexappserver.Thread, error) {
	t, err := h.Client.ReadThread(ctx, id, false)
	if err != nil {
		return nil, notFound(err)
	}
	return t, nil
}

// notFound maps the app-server's missing-thread errors, which are generic
// invalid-request errors told apart only by their message, to ErrNotFound.
func notFound(err error) error {
	var rpc *codexappserver.RPCError
	if errors.As(err, &rpc) && rpc.Code == -32600 &&
		(strings.HasPrefix(rpc.Message, "thread not loaded") || strings.HasPrefix(rpc.Message, "invalid thread id")) {
		return fmt.Errorf("%w: %s", platforms.ErrNotFound, rpc.Message)
	}
	return err
}

// threadVersion changes whenever the thread's stored history may have. The
// update time has one-second resolution, so the rollout file's size and
// modification time are included when it is readable.
func threadVersion(t *codexappserver.Thread) string {
	v := fmt.Sprintf("%d", t.UpdatedAt)
	if t.Path != "" {
		if st, err := os.Stat(t.Path); err == nil {
			v += fmt.Sprintf(":%d:%d", st.Size(), st.ModTime().UnixNano())
		}
	}
	if t.Name != "" {
		v += ":" + t.Name
	}
	return v
}

// Session maps thread metadata to an ocman session. Status is left for the
// caller to settle.
func Session(t *codexappserver.Thread) db.Session {
	s := db.Session{ID: t.ID, Platform: PlatformID, ParentID: t.ParentThreadID, Title: Title(t), Directory: t.Cwd,
		TimeCreated: t.CreatedAt * 1000, TimeUpdated: t.UpdatedAt * 1000}
	if t.GitInfo != nil {
		s.Branch = t.GitInfo.Branch
	}
	return s
}

// Title is the user's thread name, else the first line of its preview.
func Title(t *codexappserver.Thread) string {
	if t.Name != "" {
		return t.Name
	}
	title, _, _ := strings.Cut(strings.TrimSpace(t.Preview), "\n")
	if r := []rune(title); len(r) > 80 {
		title = string(r[:79]) + "…"
	}
	return title
}

// inferStatus applies the shared stored-status rule to the last message.
func inferStatus(messages []db.Message) db.SessionStatus {
	if len(messages) == 0 {
		return db.InferStoredSessionStatus("", "", "")
	}
	var info struct {
		Role   string          `json:"role"`
		Finish string          `json:"finish"`
		Error  json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(messages[len(messages)-1].Data, &info)
	errText := ""
	if len(info.Error) > 0 && string(info.Error) != "null" {
		errText = string(info.Error)
	}
	return db.InferStoredSessionStatus(info.Role, info.Finish, errText)
}
