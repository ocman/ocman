package codexprovider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

const extras = "testdata/0.164.0-extras/"

// The recorded extras thread: a plain turn and a compaction turn spent
// tokens; the shell and review turns recorded none.
func checkRecordedUsage(t *testing.T, usage map[string]TurnUsage) {
	t.Helper()
	turns := load[struct{ Data []codexappserver.Turn }](t, extras+"turns.json").Data
	first, compact := usage[turns[0].ID], usage[turns[2].ID]
	if first.Tokens.InputTokens != 14619 || first.Tokens.OutputTokens != 5 || first.Context != 14624 || first.Window <= 0 {
		t.Errorf("first turn usage = %+v", first)
	}
	if compact.Tokens.InputTokens != 14694 || compact.Tokens.OutputTokens != 353 {
		t.Errorf("compaction usage = %+v", compact)
	}
	if u, ok := usage[turns[1].ID]; ok && u.Tokens.TotalTokens != 0 {
		t.Errorf("shell turn usage = %+v", u)
	}
}

func TestReadRolloutUsage(t *testing.T) {
	usage, err := ReadRolloutUsage(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	checkRecordedUsage(t, usage)

	// Cold rollouts are zstd-compressed.
	data, err := os.ReadFile(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := zstd.NewWriter(nil)
	compressed := filepath.Join(t.TempDir(), "rollout.jsonl.zst")
	if err := os.WriteFile(compressed, enc.EncodeAll(data, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	usage, err = ReadRolloutUsage(compressed)
	if err != nil {
		t.Fatal(err)
	}
	checkRecordedUsage(t, usage)

	if _, err := ReadRolloutUsage(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("missing rollout read")
	}
	bad := filepath.Join(t.TempDir(), "bad.jsonl.zst")
	if err := os.WriteFile(bad, []byte("not zstd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRolloutUsage(bad); err == nil {
		t.Error("corrupt compressed rollout read")
	}
	// Malformed and unrelated lines are skipped.
	odd := filepath.Join(t.TempDir(), "odd.jsonl")
	if err := os.WriteFile(odd, []byte(`{"type":"token_usage_record"`+"\n"+`{"type":"event_msg","payload":{"type":"task_started"}}`+"\n"+
		`{"type":"session_meta","payload":{"type":"task_started","turn_id":"t"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if usage, err := ReadRolloutUsage(odd); err != nil || len(usage) != 0 {
		t.Errorf("odd rollout = %+v %v", usage, err)
	}
}

// TestSnapshotUsage: stored replies carry their turn's tokens, so analytics
// sees Codex usage, and the context a turn ended with for the info panel.
func TestSnapshotUsage(t *testing.T) {
	thread := load[codexappserver.ThreadReadResponse](t, extras+"thread-read.json").Thread
	data, err := os.ReadFile(extras + "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	thread.Path = filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(thread.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeHistory{threads: []*codexappserver.Thread{&thread},
		turns: load[struct{ Data []codexappserver.Turn }](t, extras+"turns.json").Data,
		items: load[struct{ Data []codexappserver.ItemEntry }](t, extras+"items.json").Data}
	snap, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	var withTokens int
	for _, m := range snap.Messages {
		info := decodeMap(t, m.Data)
		tokens, _ := info["tokens"].(map[string]any)
		if tokens != nil && tokens["input"].(float64) > 0 {
			withTokens++
			if info["contextTokens"].(float64) <= 0 || info["contextLimit"].(float64) <= 0 {
				t.Errorf("context = %v / %v", info["contextTokens"], info["contextLimit"])
			}
			// Cached input is reported apart from input, as in v1.
			if tokens["cache"].(map[string]any)["read"].(float64) < 0 {
				t.Errorf("tokens = %v", tokens)
			}
		}
	}
	if withTokens != 2 {
		t.Fatalf("%d replies carry tokens, want the turn and the compaction", withTokens)
	}
	// A rollout that cannot be read leaves history without usage.
	thread.Path = filepath.Join(t.TempDir(), "gone.jsonl")
	if _, err := (&HistoryReader{Client: f}).Snapshot(context.Background(), thread.ID); err != nil {
		t.Fatalf("snapshot without rollout = %v", err)
	}
}
