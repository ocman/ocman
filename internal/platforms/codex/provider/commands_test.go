package codexprovider

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/testutil"
)

func TestAdvertisedCommandsOutsideTurn(t *testing.T) {
	h, _ := acpHarness(t)
	notify := func(id string, commands []acp.AvailableCommand) {
		t.Helper()
		if err := h.b.acp.update(t.Context(), acp.SessionNotification{SessionId: acp.SessionId(id), Update: acp.SessionUpdate{
			AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{AvailableCommands: commands},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(dir string) []platforms.SlashCommandEntry {
		t.Helper()
		var catalog platforms.DirectoryCatalog
		if code := h.call("GET", "/catalog?directory="+dir, nil, &catalog); code != http.StatusOK {
			t.Fatalf("catalog status = %d", code)
		}
		return catalog.Commands
	}
	if got := read("/repo"); got != nil {
		t.Fatalf("commands before execution = %+v", got)
	}
	createACP(t, h)
	testutil.WaitFor(t, time.Second, "initial command announcement", func() bool { return len(read("/repo")) == 2 })
	notify("thr", []acp.AvailableCommand{{Name: "/status", Description: "Account status"}, {Name: "review-branch", Description: "Review a branch"}, {Name: "status"}, {Name: ""}})
	want := []platforms.SlashCommandEntry{{Name: "status", Description: "Account status", Source: "command"}, {Name: "review-branch", Description: "Review a branch", Source: "command"}}
	if got := read("/repo"); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %+v, want %+v", got, want)
	}
	if got := read("/other"); got != nil {
		t.Fatalf("commands leaked to another directory: %+v", got)
	}
	notify("thr", []acp.AvailableCommand{})
	if got := read("/repo"); got == nil || len(got) != 0 {
		t.Fatalf("empty update did not clear commands: %+v", got)
	}
	// Other loaded roots in the same directory may announce a newer catalog.
	h.b.mu.Lock()
	h.b.sessionLocked("second").dir = "/repo"
	h.b.mu.Unlock()
	h.b.acp.mu.Lock()
	h.b.acp.loaded["second"] = true
	h.b.acp.mu.Unlock()
	notify("second", []acp.AvailableCommand{{Name: "mcp"}})
	if got := read("/repo"); len(got) != 1 || got[0].Name != "mcp" {
		t.Fatalf("newest catalog not used: %+v", got)
	}
	h.b.acp.close()
	if got := read("/repo"); got != nil {
		t.Fatalf("dead connection retained commands: %+v", got)
	}
}
