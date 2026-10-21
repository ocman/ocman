package owned

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convevents/v1map"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func TestDiffAndCompactionReachCanonicalConversationMapper(t *testing.T) {
	for _, kind := range []string{"session.diff", "session.compacted"} {
		t.Run(kind, func(t *testing.T) {
			a, p, _ := fixture(t)
			row, err := a.canonical(t.Context(), "native")
			if err != nil {
				t.Fatal(err)
			}
			p.events = `data: {"type":"` + kind + `","properties":{"sessionID":"native"}}` + "\n\n"
			var out bytes.Buffer
			if err := a.ProxyEvents(t.Context(), row.ID, &out, nil); err != nil {
				t.Fatal(err)
			}
			mapped := v1map.New(v1map.Options{SessionID: row.ID}).Map(v1map.ParseFrame(bytes.TrimSpace(out.Bytes())))
			if len(mapped.Events) != 1 {
				t.Fatalf("%s disappeared at canonical mapper: %s", kind, out.String())
			}
		})
	}
}

func TestUnpublishedCreationFrameDoesNotDisconnectReadySibling(t *testing.T) {
	a, p, store := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "creating", PayloadHash: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	a.native = &globalEventPlatform{p}
	p.events = "data: {\"type\":\"session.created\",\"properties\":{\"info\":{\"id\":\"allocating\"}}}\n\n" +
		"data: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"native\",\"status\":{\"type\":\"busy\"}}}\n\n"
	// Metadata exists at the native boundary before publication.
	p.rows = append(p.rows, p.rows[0])
	p.rows[1].ID = "allocating"
	var out bytes.Buffer
	if err := a.ProxyEvents(t.Context(), "", &out, nil); err != nil {
		t.Fatalf("unpublished frame disconnected owner: %v", err)
	}
	if strings.Contains(out.String(), "allocating") || !strings.Contains(out.String(), row.ID) {
		t.Fatalf("sibling lost or native creation exposed: %s", out.String())
	}
	p.events = "data: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"unattributed\"}}\n\n"
	if err := a.ProxyEvents(t.Context(), "", &out, nil); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("genuine projection error was swallowed: %v", err)
	}
}
