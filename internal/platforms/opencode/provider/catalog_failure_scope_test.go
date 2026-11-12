package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
)

// A reachable machine server does not make a deleted location's catalogs
// available. Preparation must fail, not report empty successful pickers.
func TestStoredDirectoryCatalogRejectsMissingV2Location(t *testing.T) {
	dir := t.TempDir()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	var paths []string
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			return false
		}
		paths = append(paths, r.URL.Path)
		if got := r.URL.Query().Get("location[directory]"); got != dir {
			t.Errorf("scope = %q, want deleted directory %q", got, dir)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("directory should be missing: %v", err)
		}
		w.WriteHeader(http.StatusNotFound)
		writeJSONBody(w, `{"_tag":"LocationNotFoundError","message":"Location does not exist"}`)
		return true
	})
	a := nativeQueueAdapter(t, "sess-deleted", dir, f)
	got, err := a.DirectoryCatalogFromStored(t.Context(), platforms.DirectoryCatalogRequest{Directory: dir, Port: f.Port()}, nil, db.SessionDefaults{}, true)
	if err == nil {
		t.Fatalf("missing location reported success: %+v; requests=%v", got, paths)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error omits failed directory: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("failed catalog cascaded into more requests: %v", paths)
	}
	history, err := a.DirectoryCatalogFromStored(t.Context(), platforms.DirectoryCatalogRequest{Directory: dir, Port: f.Port()}, []db.RecentModel{{Provider: "p", Model: "retained"}}, db.SessionDefaults{Agent: "build", Model: "p/retained"}, false)
	if err != nil || history == nil || history.LiveConnection || history.DefaultModel != "p/retained" || len(history.Models.Models) != 1 || len(paths) != 1 {
		t.Fatalf("offline history changed or made a live request: %+v %v requests=%v", history, err, paths)
	}
	session, err := a.db.GetSession(t.Context(), "sess-deleted")
	if err != nil || session.Directory != dir {
		t.Fatalf("session identity changed: %+v %v", session, err)
	}
}

func TestStoredDirectoryCatalogPropagatesV2FailuresAndRetries(t *testing.T) {
	for _, endpoint := range []string{"/api/provider", "/api/model", "/api/agent", "/api/command"} {
		for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusOK} {
			t.Run(fmt.Sprintf("%s/%d", endpoint, status), func(t *testing.T) {
				var fail atomic.Bool
				fail.Store(true)
				var failedReads atomic.Int32
				dir := t.TempDir()
				f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
					if got := r.URL.Query().Get("location[directory]"); got != dir {
						t.Errorf("request escaped directory: %s %q", r.URL.Path, got)
					}
					if r.URL.Path == endpoint && fail.Load() {
						failedReads.Add(1)
						w.WriteHeader(status)
						writeJSONBody(w, `{invalid`)
						return true
					}
					switch r.URL.Path {
					case "/api/agent":
						writeJSONBody(w, `{"data":[{"id":"build","mode":"primary"}]}`)
					case "/api/command":
						writeJSONBody(w, `{"data":[{"name":"check"}]}`)
					case "/api/provider", "/api/model", "/api/skill":
						writeJSONBody(w, `{"data":[]}`)
					case "/api/model/default":
						writeJSONBody(w, `{"data":null}`)
					default:
						return false
					}
					return true
				})
				a := nativeQueueAdapter(t, "catalog-failure", dir, f)
				req := platforms.DirectoryCatalogRequest{Directory: dir, Port: f.Port()}
				got, err := a.DirectoryCatalogFromStored(t.Context(), req, nil, db.SessionDefaults{}, true)
				if err == nil || got != nil || !strings.Contains(err.Error(), dir) || failedReads.Load() != 1 {
					t.Fatalf("failure concealed: catalog=%+v error=%v reads=%d", got, err, failedReads.Load())
				}
				fail.Store(false)
				got, err = a.DirectoryCatalogFromStored(t.Context(), req, nil, db.SessionDefaults{}, true)
				if err != nil || got == nil || !got.LiveConnection || len(got.Agents) != 1 || len(got.Commands) != 1 {
					t.Fatalf("catalog did not recover: %+v %v", got, err)
				}
			})
		}
	}
}

func TestStoredDirectoryCatalogPreservesCancellation(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := a.DirectoryCatalogFromStored(ctx, platforms.DirectoryCatalogRequest{Directory: "/canceled", Port: "must-not-connect"}, nil, db.SessionDefaults{}, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

type catalogTimeoutTransport struct{}

func (catalogTimeoutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, os.ErrDeadlineExceeded
}

func TestStoredDirectoryCatalogPreservesTransportTimeout(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	a.httpState = &httpState{api: &http.Client{Transport: catalogTimeoutTransport{}}, catalog: newHTTPCache(time.Second)}
	_, err := a.DirectoryCatalogFromStored(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/timeout", Port: "1234"}, nil, db.SessionDefaults{}, true)
	if !errors.Is(err, os.ErrDeadlineExceeded) || !strings.Contains(err.Error(), "/timeout") {
		t.Fatalf("timeout concealed: %v", err)
	}
}
