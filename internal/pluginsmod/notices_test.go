package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/testutil"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func noticeTestDescription() plugins.Description {
	d := pagesTestDescription()
	d.Capabilities = append(d.Capabilities, plugins.ActionCapability, plugins.NoticeCapability)
	d.RequestedGrants = append(d.RequestedGrants, plugins.NoticeGrant, "context.owner")
	d.Actions = []plugins.ActionDescriptor{
		{ID: "stop-recording", Label: "Stop recording", Placement: "global", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.owner"}, Confirmation: "Stop the recording?"},
		{ID: "scan", Label: "Scan", Placement: "project", Surfaces: []string{"command-palette"}},
	}
	return d
}

func TestNoticeProcessHelper(t *testing.T) {
	if os.Getenv("NOTICE_PROCESS_HELPER") != "1" {
		return
	}
	mode := plugin.Mode(os.Args[len(os.Args)-1])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan plugin.Event)
	if mode == plugin.ModeServe {
		go func() {
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for range tick.C {
				data, err := os.ReadFile("notice-next")
				if err != nil {
					continue
				}
				_ = os.Remove("notice-next")
				if string(data) == "exit" {
					os.Exit(1)
				}
				// Send a raw frame to exercise the host's decoder independently
				// of the SDK's validated constructor.
				events <- plugin.Event{Capability: plugin.NoticeCapability.Name, Name: plugins.NoticeSetEvent, Data: data}
			}
		}()
	}
	if plugin.RunWithEvents(ctx, mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), noticeTestDescription(), os.Stdin, os.Stdout, nil, events) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestPluginNoticesFromBackendLifecycle(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteScript(t, filepath.Join(root, "ocman-plugin-notices"), "#!/bin/sh\nexport NOTICE_PROCESS_HELPER=1\nexec '"+strings.ReplaceAll(executable, "'", "'\\''")+"' -test.run='^TestNoticeProcessHelper$' -- \"$@\"\n")
	db := statetest.Open(t, Schema)
	m := bound(NewModule(Deps{Store: db}), ctx)
	t.Cleanup(m.Stop)
	if _, err := m.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	id := noticeTestDescription().ID
	grants := []string{plugins.PageProjectGrant, plugins.NoticeGrant, "context.owner"}
	if err := db.SetPluginEnabled(ctx, id, true, grants); err != nil {
		t.Fatal(err)
	}
	if err := m.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	dataDir, err := db.PluginDataDir(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	send := func(data string) {
		t.Helper()
		tmp := filepath.Join(dataDir, "notice-tmp")
		next := filepath.Join(dataDir, "notice-next")
		if err := os.WriteFile(tmp, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, next); err != nil {
			t.Fatal(err)
		}
		// The helper reads and then removes this file. Don't let the next
		// send replace it between those operations.
		testutil.WaitFor(t, 5*time.Second, "plugin helper to consume notice-next", func() bool {
			_, err := os.Stat(next)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			return errors.Is(err, os.ErrNotExist)
		})
	}
	read := func() map[string]listedPluginNotices {
		t.Helper()
		w := serve(m.handlePluginNotices, "GET", "/api/plugins/notices?ownerId=local", "", nil)
		var listed map[string]listedPluginNotices
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &listed) != nil {
			t.Fatalf("notice response: %d %s", w.Code, w.Body)
		}
		return listed
	}
	want := func(notices ...listedNotice) {
		t.Helper()
		testutil.WaitFor(t, 5*time.Second, "plugin notice state", func() bool {
			listed := read()
			if len(notices) == 0 {
				return len(listed) == 0
			}
			return len(listed) == 1 && listed[id].PluginLabel == "Pages" && reflect.DeepEqual(listed[id].Notices, notices)
		})
	}
	stop := listedNoticeAction{ActionID: "stop-recording", Label: "Stop recording", Placement: "global"}
	recording := listedNotice{ID: "recording", Tone: "warning", Title: "Recording meeting", Body: "Audio is being captured.", StartedAt: 1763540000000, Actions: []listedNoticeAction{stop}}
	const recordingEvent = `{"notices":[{"id":"recording","tone":"warning","title":"Recording meeting","body":"Audio is being captured.","startedAt":1763540000000,` +
		`"actions":[{"actionId":"stop-recording"},{"actionId":"scan"}]}]}`

	want()
	// No plugin page is mounted or called. The serve process alone publishes.
	// The project-placed action cannot run from a toast, so it is dropped.
	send(recordingEvent)
	want(recording)
	// The hub reading this owner remotely gets the notice without the action,
	// which would otherwise run on the hub instead of here.
	var remoteListed map[string]listedPluginNotices
	if r := m.RemoteOperation(ctx, remote.PluginRequest{Operation: "notices", Read: true}); r.Error != nil || json.Unmarshal(r.Value, &remoteListed) != nil ||
		len(remoteListed[id].Notices) != 1 || len(remoteListed[id].Notices[0].Actions) != 0 {
		t.Fatalf("remote notices = %+v %s", r.Error, r.Value)
	}
	// A malformed snapshot is ignored rather than clearing the valid one.
	send(`{"notices":[{"id":"recording","tone":"warning","title":"Recording","url":"https://example.com"}]}`)
	send(`{"notices":[{"id":"second","tone":"info","title":"Second"},{"id":"recording","tone":"critical","title":"Still recording"}]}`)
	want(listedNotice{ID: "second", Tone: "info", Title: "Second", Actions: []listedNoticeAction{}},
		listedNotice{ID: "recording", Tone: "critical", Title: "Still recording", Actions: []listedNoticeAction{}})
	send(`{"notices":[]}`)
	want()

	send(recordingEvent)
	want(recording)
	send("exit")
	proc := m.Process(id)
	testutil.WaitFor(t, 5*time.Second, "plugin process restart", func() bool {
		h := proc.Health()
		return h.Status == "ready" && h.RestartCount == 1
	})
	want()
	send(recordingEvent)
	want(recording)

	// Revoking the action's grant keeps the notice but removes its button.
	if err := db.SetPluginGrants(ctx, id, []string{plugins.PageProjectGrant, plugins.NoticeGrant}); err != nil {
		t.Fatal(err)
	}
	withoutAction := recording
	withoutAction.Actions = []listedNoticeAction{}
	want(withoutAction)
	// A snapshot must respect current grants even before process reconciliation.
	if err := db.SetPluginGrants(ctx, id, []string{plugins.PageProjectGrant}); err != nil {
		t.Fatal(err)
	}
	want()
	if err := db.SetPluginGrants(ctx, id, grants); err != nil {
		t.Fatal(err)
	}
	want(recording)
	if err := db.SetPluginEnabled(ctx, id, false, nil); err != nil {
		t.Fatal(err)
	}
	want()
	if err := m.SyncPluginProcesses(ctx); err != nil || m.Process(id) != nil {
		t.Fatalf("process survived disable: %v", err)
	}
}

func TestNoticeActionsResolveOnlyHubRunnableDeclarations(t *testing.T) {
	d := noticeTestDescription()
	refs := []plugins.NoticeAction{{ActionID: "missing"}, {ActionID: "scan"}, {ActionID: "stop-recording"}}
	all := []string{"context.owner"}
	if got := noticeActions(d, all, refs, false); !reflect.DeepEqual(got, []listedNoticeAction{{ActionID: "stop-recording", Label: "Stop recording", Placement: "global"}}) {
		t.Fatalf("local actions = %+v", got)
	}
	if got := noticeActions(d, nil, refs, false); len(got) != 0 {
		t.Fatalf("ungranted action listed: %+v", got)
	}
	// The hub forces global actions to itself, so a remote notice cannot offer one.
	if got := noticeActions(d, all, refs, true); got == nil || len(got) != 0 {
		t.Fatalf("remote actions = %#v", got)
	}
}

type noticeRemotes struct{ Remotes }

func (noticeRemotes) PluginOperation(_ context.Context, owner string, req remote.PluginRequest) (remote.PluginResponse, error) {
	if owner != "owner-2" {
		return remote.PluginResponse{}, platforms.ErrRemoteOffline
	}
	if req.Operation != "notices" || !req.Read {
		return remote.PluginResponse{}, errors.New("unexpected operation")
	}
	return pluginResponse(map[string]listedPluginNotices{"org.example.pages": {PluginLabel: "Pages", Notices: []listedNotice{{ID: "a", Tone: "info", Title: "A", Actions: []listedNoticeAction{}}}}}, nil), nil
}

func TestPluginNoticeOwnerRouting(t *testing.T) {
	m := NewModule(Deps{Store: statetest.Open(t, Schema), Remotes: func() Remotes { return noticeRemotes{} }})
	for _, tt := range []struct {
		query string
		code  int
		body  string
	}{
		{"", http.StatusBadRequest, ""},
		{"?ownerId=local", http.StatusOK, `{}`},
		{"?ownerId=owner-2", http.StatusOK, `{"org.example.pages":{"pluginLabel":"Pages","notices":[{"id":"a","tone":"info","title":"A","actions":[]}]}}`},
		{"?ownerId=offline", http.StatusServiceUnavailable, ""},
	} {
		w := serve(m.handlePluginNotices, "GET", "/api/plugins/notices"+tt.query, "", nil)
		if w.Code != tt.code || (tt.body != "" && strings.TrimSpace(w.Body.String()) != tt.body) {
			t.Errorf("%s = %d %s", tt.query, w.Code, w.Body)
		}
	}
	if r := m.RemoteOperation(t.Context(), remote.PluginRequest{Operation: "notices"}); r.Error == nil || r.Error.Category != plugins.ErrorInvalidArgument {
		t.Fatalf("accepted non-read RPC: %+v", r)
	}
	if r := NewModule(Deps{}).RemoteOperation(t.Context(), remote.PluginRequest{Operation: "notices", Read: true}); r.Error == nil {
		t.Fatalf("read notices without a store: %+v", r)
	}
}
