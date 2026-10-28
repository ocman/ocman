package claudeprovider

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestHistoryHeadNeverRefreshesTranscript(t *testing.T) {
	idx := openSynced(t, fixture(t))
	h := &HistoryReader{index: idx}
	before := idx.parsed.Load()
	row, version, model, err := h.Head(t.Context(), mainID)
	if err != nil || row.ID != mainID || version == "" || model == "" {
		t.Fatal("head unavailable", row, version, model, err)
	}
	e, err := idx.get(t.Context(), mainID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Path, []byte("changed, invalid transcript\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.Head(t.Context(), mainID); !errors.Is(err, db.ErrHistoryChanged) {
		t.Fatal("stale index accepted", err)
	}
	if idx.parsed.Load() != before {
		t.Fatal("metadata read parsed transcript")
	}
	if err := os.Remove(e.Path); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.Head(t.Context(), mainID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted source accepted", err)
	}
	if _, _, _, err := h.Head(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing source accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := h.Head(ctx, mainID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHistoryHeadPreservesLiveMetadataWithoutRequiringTranscript(t *testing.T) {
	config := fixture(t)
	idx := openSynced(t, config)
	opts := DefaultOptions(t.TempDir())
	opts.ConfigDir = config
	opts.StatePath = ""
	l := newLive(idx, opts, "2.1.220")
	l.owned["fresh"] = &owned{Directory: "/repo", Created: 123, Title: "Live"}
	h := &HistoryReader{index: idx, live: l}
	before := idx.parsed.Load()
	row, version, _, err := h.Head(t.Context(), "fresh")
	if err != nil || row.ID != "fresh" || !row.LiveConnection || version != "" {
		t.Fatal("unpersisted runtime metadata lost", row, version, err)
	}
	row, version, _, err = h.Head(t.Context(), mainID)
	if err != nil || !row.LiveConnection || version == "" {
		t.Fatal("resumable history metadata lost", row, version, err)
	}
	l.owned["fresh"].Config = "/other-config"
	if _, _, _, err := h.Head(t.Context(), "fresh"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign ownership leaked into source metadata", err)
	}
	if idx.parsed.Load() != before {
		t.Fatal("metadata lookup parsed native transcript")
	}
}
