package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// HistorySequences implements platforms.HistoryFeed for OpenCode v2. Older
// schemas have no durable sequence and report ErrUnsupported.
func (a *Adapter) HistorySequences(ctx context.Context) (map[string]int64, error) {
	if a.db == nil {
		return nil, fmt.Errorf("opencode history source unavailable")
	}
	seqs, err := a.db.HistorySequences(ctx)
	if errors.Is(err, db.ErrHistoryUnsupported) {
		return nil, platforms.ErrUnsupported
	}
	return seqs, err
}

func (a *Adapter) HistoryMessages(ctx context.Context, sessionID string, messageIDs []string) (*db.HistorySnapshot, error) {
	if a.db == nil {
		return nil, fmt.Errorf("opencode history source unavailable")
	}
	snap, err := a.db.HistoryMessages(ctx, sessionID, messageIDs)
	if errors.Is(err, db.ErrHistoryUnsupported) {
		return nil, platforms.ErrUnsupported
	}
	return a.historyProject(ctx, snap, err)
}

// WatchHistory follows the v2 machine server's /api/event firehose, which
// carries every location and every session the background service runs,
// including ones started in a terminal. It has no replay; the caller catches
// up through HistorySequences each time connected is called.
func (a *Adapter) WatchHistory(ctx context.Context, connected func(), change func(platforms.HistoryChange)) error {
	if a.db == nil || !a.db.V2() {
		return platforms.ErrUnsupported
	}
	port := a.rt.MachineServerPort()
	if port == "" {
		return platforms.ErrPlatformUnreachable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/api/event", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// No otelhttp: a span would cover the whole stream (see ProxyEvents).
	resp, err := (&http.Client{Transport: a.auth.Transport(http.DefaultTransport)}).Do(req)
	if err != nil {
		return fmt.Errorf("opencode history feed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opencode history feed: upstream HTTP %d", resp.StatusCode)
	}
	idle := platforms.NewIdleWatchdog(platforms.SSEIdleTimeout, func() { resp.Body.Close() })
	defer idle.Stop()
	reader := bufio.NewReaderSize(idle.Reader(resp.Body), 64<<10)
	notified := false
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data:"):
			if frame.Len() > 0 {
				frame.WriteByte('\n')
			}
			frame.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "" && frame.Len() > 0:
			event := frame.String()
			frame.Reset()
			if !notified {
				// The first frame is server.connected: everything after it is
				// live, so a catch-up started now cannot miss a change.
				notified = true
				connected()
			}
			if c, ok := historyChange([]byte(event)); ok {
				change(c)
			}
		}
		if err != nil {
			if idle.Expired() {
				return platforms.ErrSSEIdleTimeout
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

// historyChange classifies one /api/event frame. Only durable session events
// change history; ephemeral deltas and instance notices are ignored.
func historyChange(raw []byte) (platforms.HistoryChange, bool) {
	var event struct {
		Type    string `json:"type"`
		Durable *struct {
			AggregateID string `json:"aggregateID"`
			Seq         int64  `json:"seq"`
		} `json:"durable"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Durable == nil || !strings.HasPrefix(event.Durable.AggregateID, "ses") {
		return platforms.HistoryChange{}, false
	}
	c := platforms.HistoryChange{SessionID: event.Durable.AggregateID, Seq: event.Durable.Seq}
	switch {
	case event.Type == "session.deleted":
		c.Deleted = true
		return c, true
	case unknownRange(event.Type):
		c.Resync = true
		return c, true
	}
	ids := map[string]bool{}
	for key, value := range event.Data {
		if key != "messageID" && key != "inboxID" && !strings.HasSuffix(key, "MessageID") {
			continue
		}
		var id string
		if json.Unmarshal(value, &id) == nil && strings.HasPrefix(id, "msg") {
			ids[id] = true
		}
	}
	for id := range ids {
		c.MessageIDs = append(c.MessageIDs, id)
	}
	sort.Strings(c.MessageIDs)
	return c, true
}

// unknownRange lists durable events that change messages they do not name:
// reverts and compactions rewrite history, and synthetic, shell and skill
// events add messages identified only in the projected transcript.
func unknownRange(kind string) bool {
	for _, part := range []string{"revert", "compaction", "synthetic", "shell", "skill", "forked"} {
		if strings.Contains(kind, part) {
			return true
		}
	}
	return false
}
