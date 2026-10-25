package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
)

type modelCatalogPlatform struct {
	platforms.Platform
	catalog *platforms.DirectoryCatalog
	err     error
	reads   int
}

func (p *modelCatalogPlatform) DirectoryCatalog(_ context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	if req.Directory != "" || req.Port != "" {
		panic("model picker must not require a project or runtime")
	}
	p.reads++
	return p.catalog, p.err
}

func TestPluginModelCatalog(t *testing.T) {
	p := &modelCatalogPlatform{catalog: &platforms.DirectoryCatalog{Models: &platforms.SessionModelsResponse{Models: []platforms.SessionModel{{Provider: "p", Model: "m"}}}}}
	m := NewModule(Deps{
		Local: func() hostsvc.Host { return nil },
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, requested, model string) (string, string, error) {
			if requested != "platform-a" || model != "" {
				return "", "", errors.New("invalid platform")
			}
			return requested, requested, nil
		},
		Platform: func(id string) (platforms.Platform, bool) { return p, id == "platform-a" },
	})
	input := remote.PluginInput{Values: map[string]json.RawMessage{"platform": json.RawMessage(`"platform-a"`)}}
	response := m.RemoteOperation(t.Context(), remote.PluginRequest{Operation: "model-catalog", Input: input})
	if response.Error != nil || p.reads != 1 {
		t.Fatalf("owner-local catalog failed: %+v", response)
	}
	var models platforms.SessionModelsResponse
	if json.Unmarshal(response.Value, &models) != nil || len(models.Models) != 1 || models.Models[0].Model != "m" {
		t.Fatalf("wrong catalog: %s", response.Value)
	}
	for _, catalog := range []*platforms.DirectoryCatalog{nil, {}} {
		p.catalog = catalog
		if result, err := m.pluginModelCatalog(t.Context(), input); err != nil || result.Models == nil || len(result.Models) != 0 {
			t.Fatalf("empty catalog: %+v %v", result, err)
		}
	}
	p.err = errors.New("private provider diagnostic")
	if _, err := m.pluginModelCatalog(t.Context(), input); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("unsafe catalog error: %v", err)
	}
	p.err = nil
	m.deps.Platform = func(string) (platforms.Platform, bool) { return nil, false }
	if _, err := m.pluginModelCatalog(t.Context(), input); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatal("unknown platform accepted")
	}
	input.Values["platform"] = json.RawMessage(`"foreign"`)
	if _, err := m.pluginModelCatalog(t.Context(), input); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatal("platform resolver error ignored")
	}
	if _, err := m.pluginModelCatalog(t.Context(), remote.PluginInput{}); !errors.Is(err, plugins.ErrInvalidMessage) {
		t.Fatal("missing platform accepted")
	}
	m = NewModule(Deps{})
	input.Values["platform"] = json.RawMessage(`"platform-a"`)
	if _, err := m.pluginModelCatalog(t.Context(), input); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatal("missing dependencies accepted")
	}
}

func TestPluginModelCatalogHTTPFailsClosedForOfflineOwner(t *testing.T) {
	m := NewModule(Deps{})
	for _, tc := range []struct {
		query string
		code  int
	}{
		{"", 400}, {"?ownerId=local", 400}, {"?platform=a", 400},
		{"?ownerId=offline&platform=a", 503}, {"?ownerId=local&platform=a", 503},
	} {
		w := httptest.NewRecorder()
		m.handlePluginModelCatalog(w, httptest.NewRequest(http.MethodGet, "/api/plugins/model-catalog"+tc.query, nil))
		if w.Code != tc.code {
			t.Fatalf("%s: got %d want %d", tc.query, w.Code, tc.code)
		}
	}
}
