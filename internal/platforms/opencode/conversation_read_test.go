package opencode

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestConversationHeadUsesBoundedMetadata(t *testing.T) {
	d := newTestDBWithSession(t, "s", "/repo")
	a := New(d, nil)
	h, err := a.ConversationHead(t.Context(), "s")
	if err != nil || h.Session.ID != "s" || h.Version == "" || h.Identity.SourceKey == "" || !h.DirectoryDefaults {
		t.Fatalf("head: %+v %v", h, err)
	}
	h, err = a.ConversationHead(t.Context(), "missing")
	if !errors.Is(err, platforms.ErrNotFound) || h == nil || h.Identity.SourceKey == "" {
		t.Fatal("missing source identity lost", h, err)
	}
	if _, err := New(nil, nil).ConversationHead(t.Context(), "s"); err == nil {
		t.Fatal("missing source accepted")
	}
}
