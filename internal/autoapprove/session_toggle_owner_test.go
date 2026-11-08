package autoapprove

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

// toggleOwnerPlatform is a remote adapter whose owner stores the toggle.
type toggleOwnerPlatform struct {
	*fakePlatform
	owner *AutoApproveState
	calls []*bool
	err   error
}

func (p *toggleOwnerPlatform) SessionAutoApprove(_ context.Context, id string, enabled *bool) (AutoApproveState, error) {
	p.calls = append(p.calls, enabled)
	if p.err != nil {
		return AutoApproveState{}, p.err
	}
	if enabled != nil {
		*p.owner = AutoApproveState{Enabled: *enabled, Overridden: true}
	}
	return *p.owner, nil
}

// Regression: the hub stored a remote session's toggle in its own state.db,
// which the owner's judge never reads, so the switch silently did nothing.
func TestRemoteSessionAutoApproveToggleRoutesToOwner(t *testing.T) {
	store := statetest.Open(t)
	adapter := &toggleOwnerPlatform{fakePlatform: &fakePlatform{id: "r-abc:opencode"}, owner: &AutoApproveState{}}
	m := newTestModule(store)
	m.Session = func(http.ResponseWriter, *http.Request, string) platforms.Platform { return adapter }

	if rec := serve(m, "POST", "/api/session/s1/auto-approve", `{"enabled":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set = %d %s", rec.Code, rec.Body)
	}
	if len(adapter.calls) != 1 || adapter.calls[0] == nil || !*adapter.calls[0] {
		t.Fatalf("owner calls = %v", adapter.calls)
	}
	if _, exists, err := store.GetAutoApprove(t.Context(), "r-abc:opencode", "s1"); err != nil || exists {
		t.Fatalf("hub stored the remote toggle: exists=%v err=%v", exists, err)
	}

	rec := serve(m, "GET", "/api/session/s1/auto-approve", "")
	var got struct{ Enabled, Overridden bool }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || !got.Enabled || !got.Overridden {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if len(adapter.calls) != 2 || adapter.calls[1] != nil {
		t.Fatalf("read did not ask the owner: %v", adapter.calls)
	}
}

func TestRemoteSessionAutoApproveToggleFailsClosed(t *testing.T) {
	store := statetest.Open(t)
	m := newTestModule(store)
	cases := map[string]platforms.Platform{
		// An owner without the RPC must not fall back to the hub's database.
		"unsupported owner": &toggleOwnerPlatform{fakePlatform: &fakePlatform{id: "r-abc:opencode"}, owner: &AutoApproveState{}, err: platforms.ErrUnsupported},
		// A remote adapter without the method at all.
		"no owner method": &fakePlatform{id: "r-abc:opencode"},
	}
	for name, adapter := range cases {
		t.Run(name, func(t *testing.T) {
			m.Session = func(http.ResponseWriter, *http.Request, string) platforms.Platform { return adapter }
			if rec := serve(m, "POST", "/api/session/s1/auto-approve", `{"enabled":true}`); rec.Code < 400 {
				t.Fatalf("set = %d", rec.Code)
			}
			if _, exists, _ := store.GetAutoApprove(t.Context(), "r-abc:opencode", "s1"); exists {
				t.Fatal("hub stored the remote toggle")
			}
		})
	}
}

func TestLocalSessionAutoApproveToggleStaysLocal(t *testing.T) {
	store := statetest.Open(t)
	m := newTestModule(store)
	m.DefaultEnabled = func() bool { return false }
	adapter := manualPolicyPlatform{fakePlatform: &fakePlatform{id: "opencode"}}
	state, err := m.SessionAutoApprove(t.Context(), adapter, "s1", nil)
	if err != nil || state.Enabled || state.Overridden {
		t.Fatalf("default = %+v %v", state, err)
	}
	on := true
	if state, err = m.SessionAutoApprove(t.Context(), adapter, "s1", &on); err != nil || !state.Enabled || !state.Overridden {
		t.Fatalf("set = %+v %v", state, err)
	}
	if enabled, exists, _ := store.GetAutoApprove(t.Context(), "opencode", "s1"); !enabled || !exists {
		t.Fatal("local toggle not stored")
	}
}
