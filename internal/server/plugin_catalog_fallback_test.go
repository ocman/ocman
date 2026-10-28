package server

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestLiveCatalogSearchesLaterAdapters(t *testing.T) {
	for _, failure := range []string{"error", "nil", "historical"} {
		t.Run(failure, func(t *testing.T) {
			r := platforms.NewRegistry()
			r.Register(&fakePlatform{id: "claudecode", directoryCatalogFn: func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
				switch failure {
				case "error":
					return nil, errors.New("unavailable")
				case "nil":
					return nil, nil
				default:
					return &platforms.DirectoryCatalog{}, nil
				}
			}})
			live := &platforms.DirectoryCatalog{LiveConnection: true, DefaultAgent: "build"}
			r.Register(&fakePlatform{id: "opencode", directoryCatalogFn: func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) { return live, nil }})
			s := &Server{registry: r}
			if got := s.liveCatalog(t.Context(), "/repo"); got != live {
				t.Fatalf("catalog = %+v, want live catalog", got)
			}
		})
	}
}
