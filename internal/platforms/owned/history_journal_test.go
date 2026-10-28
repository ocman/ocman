package owned

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/historyjournal"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type journalPlatform struct {
	*nativePlatform
	dir string
}

func (p *journalPlatform) HistoryJournalDir() string { return p.dir }

func TestJournalReadsKeepOffsetsAndHideUnassociatedNativeReferences(t *testing.T) {
	a, p, _ := fixture(t)
	j := &journalPlatform{nativePlatform: p, dir: t.TempDir()}
	a.native = j
	for _, id := range []string{"native", "other-configuration"} {
		if err := historyjournal.Append(j.dir, "hooks.jsonl", historyjournal.Record{Session: id, Type: "Stop", Data: json.RawMessage(`{"sessionID":"opaque-native"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.OpenFile(filepath.Join(j.dir, "hooks.jsonl"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("invalid JSON\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := a.HistoryJournalEntries(t.Context(), "hooks.jsonl", 0, 10)
	if err != nil || len(entries) != 3 {
		t.Fatalf("journal: %+v %v", entries, err)
	}
	if !sessioncatalog.IsID(entries[0].Record.Session) || string(entries[0].Record.Data) != `{"sessionID":"opaque-native"}` || entries[1].Record.Session != "" || entries[2].Err == nil || entries[2].Record.Session != "" {
		t.Fatalf("journal identity conversion: %+v", entries)
	}
	if entries[0].Offset != 0 || entries[1].Offset != entries[0].End || entries[2].Offset != entries[1].End {
		t.Fatal("source byte offsets changed during conversion")
	}
	resumed, err := a.HistoryJournalEntries(t.Context(), "hooks.jsonl", entries[0].End, 10)
	if err != nil || len(resumed) != 2 || resumed[0].Offset != entries[0].End {
		t.Fatalf("journal resume: %+v %v", resumed, err)
	}
	if _, err := a.HistoryJournalEntries(t.Context(), "hooks.jsonl", entries[2].End+1, 10); !errors.Is(err, historyjournal.ErrGap) {
		t.Fatalf("journal gap was hidden: %v", err)
	}
}

func TestJournalDoesNotAcknowledgeUnfinishedCreation(t *testing.T) {
	a, p, store := fixture(t)
	j := &journalPlatform{nativePlatform: p, dir: t.TempDir()}
	a.native = j
	_, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := historyjournal.Append(j.dir, "hooks.jsonl", historyjournal.Record{Session: "native", Type: "Stop"}); err != nil {
		t.Fatal(err)
	}
	if entries, err := a.HistoryJournalEntries(t.Context(), "hooks.jsonl", 0, 10); !errors.Is(err, sessioncatalog.ErrCreating) || entries != nil {
		t.Fatalf("unfinished creation advanced hook records: %+v %v", entries, err)
	}
}
