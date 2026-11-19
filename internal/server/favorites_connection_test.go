package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestFavoritesNamedConnectionRoundTrip(t *testing.T) {
	for _, route := range []string{"cnx_work", "opencode"} {
		t.Run(route, func(t *testing.T) {
			testFavoritesConnectionRoundTrip(t, route)
		})
	}
}

func testFavoritesConnectionRoundTrip(t *testing.T, route string) {
	t.Helper()
	core := statetest.Open(t, ModuleSchemas()...)
	reg := platforms.NewRegistry()
	if err := reg.RegisterConnection("cnx_work", &fakePlatform{PlatformID: "opencode"}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", reg, nil)
	// Existing favorites predate named connections and must remain removable.
	if err := core.AddModelFavorite(t.Context(), "opencode", "anthropic", "opus"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodDelete, http.MethodPost} {
		rec := httptest.NewRecorder()
		body := `{"platform":"` + route + `","provider":"anthropic","model":"opus"}`
		serveModule(s, s.favoritesModule(), rec, httptest.NewRequest(method, "/api/favorites", strings.NewReader(body)))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s = %d: %s", method, rec.Code, rec.Body.String())
		}
		rows, err := core.ModelFavorites(t.Context(), "opencode")
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if method == http.MethodDelete {
			want = 0
		}
		if len(rows) != want {
			t.Fatalf("%s: catalog favorites = %+v, want %d", method, rows, want)
		}
		for _, alias := range []string{"cnx_work", "opencode"} {
			listed := httptest.NewRecorder()
			serveModule(s, s.favoritesModule(), listed, httptest.NewRequest(http.MethodGet, "/api/favorites?platform="+alias, nil))
			var favorites []struct{ Platform, Provider, Model string }
			if listed.Code != http.StatusOK {
				t.Fatalf("GET %s = %d: %s", alias, listed.Code, listed.Body.String())
			}
			if err := json.Unmarshal(listed.Body.Bytes(), &favorites); err != nil {
				t.Fatal(err)
			}
			if len(favorites) != want || want > 0 && favorites[0].Platform != "opencode" {
				t.Fatalf("GET %s after %s = %+v", alias, method, favorites)
			}
		}
	}
}

func TestFavoritePlatformResolution(t *testing.T) {
	reg := platforms.NewRegistry()
	if err := reg.RegisterConnection("cnx_work", &fakePlatform{PlatformID: "opencode"}); err != nil {
		t.Fatal(err)
	}
	remote := &createNamedRemote{Fake: &fakePlatform{PlatformID: "r-box:cnx_work"}, kind: "opencode"}
	if err := reg.RegisterConnection("r-box:cnx_work", remote); err != nil {
		t.Fatal(err)
	}
	s := &Server{registry: reg}
	for _, tt := range []struct {
		route, want string
		valid       bool
	}{
		{"cnx_work", "opencode", true},
		{"opencode", "opencode", true},
		{"r-box:cnx_work", "r-box:opencode", true},
		{"r-box:opencode", "r-box:opencode", true},
		{"cnx_missing", "", false},
		{"r-offline:cnx_work", "", false},
		{"r-offline:opencode", "", false},
		{"claudecode", "", false},
	} {
		t.Run(tt.route, func(t *testing.T) {
			got, valid := s.favoritePlatform(tt.route)
			if got != tt.want || valid != tt.valid {
				t.Fatalf("resolution = %q, %v; want %q, %v", got, valid, tt.want, tt.valid)
			}
		})
	}
	if _, ok := (&Server{}).favoritePlatform("opencode"); ok {
		t.Fatal("accepted a platform without a registry")
	}
}
