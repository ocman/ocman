package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func TestAdapterConfigurationCataloguesUseOwnDiscovery(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	for _, healthy := range []bool{true, false} {
		t.Run(map[bool]string{true: "healthy", false: "unavailable"}[healthy], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !healthy {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				switch r.URL.Path {
				case "/agent":
					writeJSONBody(w, `[{"name":"custom","mode":"primary"}]`)
				case "/provider":
					writeJSONBody(w, `{"all":[{"id":"p","models":{"model":{}}}],"connected":["p"]}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			t.Cleanup(func() { catalogCache.invalidatePort(u.Port()) })
			a := New(nil, nil)
			a.rt = NewDiscovery()
			a.Discovery().SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{"/repo": u.Port()} })
			models := a.ModelOptions(t.Context())
			if healthy && !reflect.DeepEqual(models, []string{"p/model"}) || !healthy && len(models) != 0 {
				t.Fatal(models)
			}
			agents := a.AgentOptions(t.Context(), nil)
			want := []string{"build", "plan"}
			if healthy {
				want = []string{"build", "custom", "plan"}
			}
			if !reflect.DeepEqual(agents, want) {
				t.Fatal(agents)
			}
		})
	}
}
