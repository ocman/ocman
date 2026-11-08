package autoapprove

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/state"
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

// Regression: clearing the hub's judge model left the judge on the old one,
// because each judgment only replaced the cached model on a successful load.
func TestManagedJudgeModelClearedRestoresDefault(t *testing.T) {
	store := statetest.Open(t)
	if err := store.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := NewService(Deps{Store: store})
	s.judge = newPermissionJudge(ocapi.Auth{})
	apply := func(model string) {
		t.Helper()
		if err := store.ApplyManagedSettings(t.Context(), state.ManagedSettings{Hub: "hub-1", JudgeModel: model}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.resolveJudgeInputs(t.Context(), "/repo", nil, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	apply("custom/model")
	if p, m := s.judge.model(); p != "custom" || m != "model" {
		t.Fatalf("custom model = %s/%s", p, m)
	}
	apply("")
	if p, m := s.judge.model(); p != judgeModelProvider || m != judgeModelID {
		t.Fatalf("cleared model = %s/%s, want the default", p, m)
	}
}
