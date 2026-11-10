package sessionlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

type namespaceSessions struct {
	Sessions
	kind          platforms.ID
	err           error
	catalog       *platforms.DirectoryCatalog
	cooldownScope string
}

func (s *namespaceSessions) PlatformKind(context.Context, string) (platforms.ID, error) {
	return s.kind, s.err
}
func (*namespaceSessions) KnownPlatform(string) bool      { return true }
func (*namespaceSessions) CooldownUntil(string) time.Time { return time.Time{} }
func (s *namespaceSessions) DirectoryCatalog(context.Context, string, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error) {
	return s.catalog, "cnx_work", nil
}
func (s *namespaceSessions) ConnectionCooldownUntil(connection, _ string) time.Time {
	s.cooldownScope = connection
	return time.Time{}
}

func TestNamedCatalogKeepsNativeModelNamespace(t *testing.T) {
	s := &namespaceSessions{kind: "claudecode"}
	m := NewModule(Deps{Sessions: func() Sessions { return s }, Platforms: func() []platforms.Platform {
		t.Fatal("named catalogue must not merge another account's model inventory")
		return nil
	}})
	models := m.MachineModels(t.Context(), "cnx_work", "/repo", []platforms.SessionModel{{Model: "haiku", Provider: "anthropic"}})
	if len(models) != 1 || models[0].Platform != "claudecode" || models[0].PlatformName != platforms.DisplayName("claudecode") {
		t.Fatalf("routing ID leaked into native model namespace: %+v", models)
	}
	s.err = errors.New("selected connection unavailable")
	if got := m.MachineModels(t.Context(), "cnx_work", "/repo", models); got != nil {
		t.Fatal("unknown selected connection borrowed a model namespace")
	}
}

func TestNamedPrepareReturnsConnectionButQualifiesDefaultWithNativeKind(t *testing.T) {
	stale := time.Now().Add(time.Hour)
	s := &namespaceSessions{kind: "claudecode", catalog: &platforms.DirectoryCatalog{
		DefaultModel: "anthropic/haiku",
		Models:       &platforms.SessionModelsResponse{Models: []platforms.SessionModel{{Model: "haiku", Provider: "anthropic", CooldownUntil: &stale}}},
	}}
	m := NewModule(Deps{
		Sessions: func() Sessions { return s },
		Owner:    func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return nil, true },
		CreatePlatform: func(context.Context, hostsvc.Host, string, string) (string, string, error) {
			return "cnx_work", "", nil
		},
		ProjectDefaultModel: func(context.Context, string) string { return "" },
	})
	w := httptest.NewRecorder()
	m.HandlePrepareSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/prepare", strings.NewReader(`{"platform":"cnx_work","directory":"/repo","remoteId":"local"}`)))
	var response struct {
		Platform     string                          `json:"platform"`
		DefaultModel string                          `json:"defaultModel"`
		Models       platforms.SessionModelsResponse `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("prepare: %d %s %v", w.Code, w.Body.String(), err)
	}
	if w.Code != http.StatusOK || response.Platform != "cnx_work" || response.DefaultModel != "claudecode:anthropic/haiku" || response.Models.Models[0].Platform != "claudecode" || response.Models.Models[0].CooldownUntil != nil || s.cooldownScope != "cnx_work" {
		t.Fatalf("prepare conflated account routing, model kind or cooldown: %+v scope=%s", response, s.cooldownScope)
	}
	s.err = platforms.ErrPlatformUnreachable
	w = httptest.NewRecorder()
	m.HandlePrepareSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/prepare", strings.NewReader(`{"platform":"cnx_work","directory":"/repo","remoteId":"local"}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing model namespace did not fail closed: %d %s", w.Code, w.Body.String())
	}
}
