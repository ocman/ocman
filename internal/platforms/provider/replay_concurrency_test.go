package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestSnapshotBrowserCannotAdvanceHeadlessCursor(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.hello.Capabilities.Replay = true
	f.reply("GET", "/sessions/s/snapshot", providerprotocol.Snapshot{Cursor: 100, Lease: "lease"})
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: 101\ndata: {\"type\":\"message.part.delta\",\"properties\":{\"sessionID\":\"s\",\"delta\":\"new\"}}\n\n")
	}
	a := f.adapter(t)
	if err := a.ProxyEvents(t.Context(), "s", io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	if a.seq != 0 {
		t.Fatal("browser skipped headless backlog", a.seq)
	}
	seen := 0
	a.SetEventHandler(func(providerprotocol.Event) { seen++ })
	if _, err := a.observe(1, providerprotocol.Event{Type: "permission.asked", Properties: json.RawMessage(`{"sessionID":"other","id":"p"}`)}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 || len(a.permissions) != 1 {
		t.Fatal("headless event skipped", seen)
	}
}

type observingWriter struct {
	bytes.Buffer
	onWrite func()
}

func (w *observingWriter) Write(p []byte) (int, error) {
	if w.onWrite != nil {
		w.onWrite()
		w.onWrite = nil
	}
	return w.Buffer.Write(p)
}

func TestPromptReplyDuringSnapshotHandoffReachesBrowser(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.hello.Capabilities.Replay = true
	asked := providerprotocol.Event{Type: "permission.asked", Properties: json.RawMessage(`{"sessionID":"s","id":"p"}`)}
	replied := providerprotocol.Event{Type: "permission.replied", Properties: json.RawMessage(`{"sessionID":"s","requestID":"p"}`)}
	f.reply("GET", "/sessions/s/snapshot", providerprotocol.Snapshot{Cursor: 1, Lease: "lease", Events: []providerprotocol.Event{asked}})
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(replied)
		_, _ = fmt.Fprintf(w, "id: 2\ndata: %s\n\n", data)
	}
	a := f.adapter(t)
	w := &observingWriter{onWrite: func() {
		if _, err := a.observe(2, replied); err != nil {
			t.Fatal(err)
		}
	}}
	if err := a.ProxyEvents(t.Context(), "s", w, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "permission.replied") {
		t.Fatal("reply lost in snapshot handoff", w.String())
	}
}

func TestHeadlessReconnectRetainsLimitedState(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.hello.Capabilities.Replay = true
	f.frames(`{"type":"session.idle","properties":{"sessionID":"s"}}`)
	a := f.adapter(t)
	a.live["s"] = platforms.LiveState{Status: string(db.StatusLimited)}
	if err := a.streamURL(t.Context(), "", 0, io.Discard, nil, "/events?consumer=headless&after=0", true); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls {
		if strings.HasSuffix(call.path, "/ack") {
			t.Fatal("acknowledged unfinished limited turn")
		}
	}
}
