package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

type prepareSelectedSessions struct{ namespaceSessions }

func (*prepareSelectedSessions) DirectoryCatalog(context.Context, string, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error) {
	return &platforms.DirectoryCatalog{}, "opencode", nil
}

func TestSelectedPreparationDoesNotCallSiblingCatalogs(t *testing.T) {
	s := &prepareSelectedSessions{namespaceSessions{kind: "opencode"}}
	sibling := &platformtest.Fake{PlatformID: "claudecode", Caps: platforms.Capabilities{Composer: true}, DirectoryCatalogFn: func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		t.Error("optional sibling catalog blocked selected-platform preparation")
		return nil, nil
	}}
	m := NewModule(Deps{
		Sessions: func() Sessions { return s },
		Owner:    func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return nil, true },
		CreatePlatform: func(context.Context, hostsvc.Host, string, string) (string, string, error) {
			return "opencode", "", nil
		},
		ProjectDefaultModel: func(context.Context, string) string { return "" },
		Platforms:           func() []platforms.Platform { return []platforms.Platform{sibling} },
	})
	w := httptest.NewRecorder()
	m.HandlePrepareSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/prepare", strings.NewReader(`{"directory":"/repo","platform":"opencode","selectedOnly":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	var result struct{ AdditionalModels bool }
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.AdditionalModels {
		t.Fatalf("optional inventory was not advertised: %s %v", w.Body.String(), err)
	}
}

func TestAdditionalModelsRespectsOwnerAndAccountScope(t *testing.T) {
	for _, tc := range []struct {
		name, id, selected string
		composer           bool
	}{
		{"selected platform", "opencode", "opencode", true},
		{"another owner", "r-box:claudecode", "opencode", true},
		{"not composer capable", "claudecode", "opencode", false},
		{"named account", "claudecode", "cnx_work", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModule(Deps{Platforms: func() []platforms.Platform {
				return []platforms.Platform{&platformtest.Fake{PlatformID: tc.id, Caps: platforms.Capabilities{Composer: tc.composer}}}
			}})
			if m.hasAdditionalModels(tc.selected, platforms.OpenCodeID) {
				t.Fatal("unrelated or named account acquired sibling inventory")
			}
		})
	}
}
