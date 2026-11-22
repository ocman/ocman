package codexprovider

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"

	appserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/rawhistory"
)

const replayFormat = "codex-appserver-v1"

type replayItem struct {
	TurnID                     string
	StartedAtMs, CompletedAtMs int64
	Item                       json.RawMessage
}

type replayInput struct {
	Thread *appserver.Thread
	Turns  []appserver.Turn
	Items  []replayItem
	Usage  map[string]TurnUsage
}

func captureCodexReplay(thread *appserver.Thread, turns []appserver.Turn, items []appserver.ItemEntry, usage map[string]TurnUsage, updated int64) (*rawhistory.Snapshot, error) {
	input := replayInput{Thread: thread, Turns: turns, Usage: usage, Items: make([]replayItem, 0, len(items))}
	for _, entry := range items {
		raw := entry.Item.Raw
		if len(raw) == 0 {
			var err error
			raw, err = json.Marshal(entry.Item)
			if err != nil {
				return nil, err
			}
		}
		input.Items = append(input.Items, replayItem{entry.TurnID, entry.StartedAtMs, entry.CompletedAtMs, raw})
	}
	return rawhistory.EncodeJSON(replayFormat, updated, input)
}

// ReplayHistory runs the normal converter over archived input without the app-server.
func ReplayHistory(archive rawhistory.Snapshot) (*Converter, error) {
	if archive.Format != replayFormat {
		return nil, fmt.Errorf("invalid Codex replay format")
	}
	r, err := gzip.NewReader(bytes.NewReader(archive.GZIP))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var input replayInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return nil, err
	}
	if input.Thread == nil {
		return nil, fmt.Errorf("missing Codex replay thread")
	}
	items := make([]appserver.ItemEntry, 0, len(input.Items))
	for _, item := range input.Items {
		entry := appserver.ItemEntry{TurnID: item.TurnID, StartedAtMs: item.StartedAtMs, CompletedAtMs: item.CompletedAtMs}
		if err := json.Unmarshal(item.Item, &entry.Item); err != nil {
			return nil, err
		}
		items = append(items, entry)
	}
	c := &Converter{Usage: input.Usage}
	c.AddHistory(input.Thread, input.Turns, items)
	return c, nil
}
