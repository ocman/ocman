package pluginsmod

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/ocman/ocman/internal/remote"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// listedNoticeAction is a declared action resolved by the host. The browser
// renders the declared label, never text from the notice event.
type listedNoticeAction struct {
	ActionID  string `json:"actionId"`
	Label     string `json:"label"`
	Placement string `json:"placement"`
}

type listedNotice struct {
	ID        string               `json:"id"`
	Tone      string               `json:"tone"`
	Title     string               `json:"title"`
	Body      string               `json:"body,omitempty"`
	StartedAt int64                `json:"startedAt,omitempty"`
	Actions   []listedNoticeAction `json:"actions"`
}

type listedPluginNotices struct {
	PluginLabel string         `json:"pluginLabel"`
	Notices     []listedNotice `json:"notices"`
}

func (m *Module) handlePluginNotices(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("ownerId")
	if owner == "" {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	writePluginResponse(w, m.RouteOperation(ctx, owner, remote.PluginRequest{Operation: "notices", Read: true}))
}

// localPluginNotices reads live snapshots without calling any plugin. Each
// snapshot is checked against the current grants and its action references
// are resolved against the current declaration.
func (m *Module) localPluginNotices(ctx context.Context, fromRemote bool) (map[string]listedPluginNotices, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil, plugins.ErrUnavailable
	}
	listed := make(map[string]listedPluginNotices)
	for id, process := range m.processes {
		if len(process.Notices()) == 0 {
			continue
		}
		err := m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
			if !d.NoticeAllowed(grants) || (fromRemote && d.Scope != plugins.ScopeOwner) {
				return nil
			}
			var notices []listedNotice
			for _, n := range process.Notices() {
				notices = append(notices, listedNotice{ID: n.ID, Tone: n.Tone, Title: n.Title, Body: n.Body, StartedAt: n.StartedAt,
					Actions: noticeActions(d, grants, n.Actions, fromRemote)})
			}
			if len(notices) > 0 {
				listed[id] = listedPluginNotices{PluginLabel: d.Name, Notices: notices}
			}
			return nil
		})
		if err != nil && !errors.Is(err, plugins.ErrUnavailable) {
			return nil, err
		}
	}
	return listed, nil
}

// noticeActions keeps only references the browser can invoke from any route:
// declared, granted, global command-palette actions. A global action always
// runs on the hub, so a remote owner's notice cannot offer one.
func noticeActions(d plugins.Description, grants []string, refs []plugins.NoticeAction, fromRemote bool) []listedNoticeAction {
	actions := []listedNoticeAction{}
	if fromRemote {
		return actions
	}
	for _, ref := range refs {
		i := slices.IndexFunc(d.Actions, func(a plugins.ActionDescriptor) bool { return a.ID == ref.ActionID })
		if i < 0 {
			continue
		}
		a := d.Actions[i]
		if a.Placement == "global" && slices.Contains(a.Surfaces, "command-palette") && a.Granted(grants) {
			actions = append(actions, listedNoticeAction{ActionID: a.ID, Label: a.Label, Placement: a.Placement})
		}
	}
	return actions
}
