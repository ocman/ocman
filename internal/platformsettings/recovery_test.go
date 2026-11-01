package platformsettings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state/statetest"
)

func TestRecoveryReloadWaitsForAuthoritativeIdleWithoutFencingNewWork(t *testing.T) {
	m := New(nil)
	reloads := 0
	idle := false
	m.Configure(func() { reloads++ }, []string{"opencode"}, false, func(context.Context) (bool, error) { return idle, nil })
	finish, err := m.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !m.RequestRecoveryReload() {
		t.Fatal("configured reload refused")
	}
	if m.tryReload(t.Context()) || reloads != 0 {
		t.Fatal("reload interrupted admitted work")
	}
	// Recovery bindings are already durable, so a busy machine must keep
	// accepting new sessions and turns instead of waiting for a global idle.
	fresh, err := m.Begin(t.Context())
	if err != nil {
		t.Fatalf("recovery reload fenced new work: %v", err)
	}
	fresh()
	finish()
	if m.tryReload(t.Context()) {
		t.Fatal("reload ignored busy runtime")
	}
	idle = true
	raced, err := m.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if m.tryReload(t.Context()) || reloads != 0 {
		t.Fatal("reload ran under admitted work")
	}
	raced()
	if !m.tryReload(t.Context()) || reloads != 1 {
		t.Fatalf("settled recovery did not reload: %d", reloads)
	}
	withoutReload := New(nil)
	if withoutReload.RequestRecoveryReload() {
		t.Fatal("invented reload callback")
	}
	finish, err = withoutReload.Begin(t.Context())
	if err != nil {
		t.Fatal("missing callback fenced work", err)
	}
	finish()
}

func TestSavingUnchangedPlatformSelectionCannotCancelRecovery(t *testing.T) {
	core := statetest.Open(t)
	m := New(core)
	m.Configure(func() {}, []string{"opencode"}, false, func(context.Context) (bool, error) { return true, nil })
	m.RequestRecoveryReload()
	w := httptest.NewRecorder()
	m.settings(w, httptest.NewRequest(http.MethodPost, "/api/settings/platforms", strings.NewReader(`{"enabled":["opencode"]}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reloadPending":true`) || !m.recoveryPending {
		t.Fatalf("settings cancelled recovery: %d %s", w.Code, w.Body.String())
	}
}
