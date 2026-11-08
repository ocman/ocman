package autoapprove

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state/statetest"
)

// A managed remote's own Settings page cannot change what its hub supplies.
func TestManagedSettingsWritesConflict(t *testing.T) {
	store := statetest.Open(t)
	if err := store.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(store)
	for path, body := range map[string]string{
		"/api/settings/prompt-sections": `[]`,
		"/api/settings/judge-delay":     `{"delayMs":5}`,
		"/api/settings/judge-model":     `{"model":"p/m"}`,
	} {
		rec := serve(m, "POST", path, body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "managed by the hub") {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestDefaultEnabledFuncOverridesFlag(t *testing.T) {
	on := true
	s := NewService(Deps{DefaultEnabled: false, DefaultEnabledFunc: func() bool { return on }})
	if !s.defaultEnabled() {
		t.Fatal("func ignored")
	}
	on = false
	if s.defaultEnabled() {
		t.Fatal("func not read per call")
	}
	if !NewService(Deps{DefaultEnabled: true}).defaultEnabled() {
		t.Fatal("flag ignored without func")
	}
}
