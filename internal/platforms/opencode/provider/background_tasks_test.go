package opencode

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

func TestAbortIndividualShell(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	const sid, dir = "ses_task_stop", "/repo/worktree with spaces"
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/shell":
			if r.URL.Query().Get("location[directory]") != dir {
				t.Error(r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"sh_one","status":"running","metadata":{"sessionID":"ses_task_stop"}}]}`))
			return true
		case "DELETE /api/shell/sh_one":
			if r.URL.Query().Get("location[directory]") != dir {
				t.Error(r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		return false
	})
	a := nativeQueueAdapter(t, sid, dir, f)
	if !a.Capabilities().BackgroundTaskStop {
		t.Fatal("missing capability")
	}
	if err := a.Abort(t.Context(), platforms.AbortRequest{SessionID: sid, TaskID: "sh_one"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.find(http.MethodDelete, "/api/shell/sh_one"); !ok {
		t.Fatal("shell was not stopped")
	}
	if _, ok := f.find(http.MethodPost, "/api/session/"+sid+"/abort"); ok {
		t.Fatal("aborted entire session")
	}
}

func TestTaskStopUnsupportedOnV1(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	a := New(nil, nil, nil, ocapi.Auth{})
	if a.Capabilities().BackgroundTaskStop {
		t.Fatal("v1 advertises task stopping")
	}
	if err := a.Abort(t.Context(), platforms.AbortRequest{SessionID: "s1", TaskID: "one"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestTaskStopUnknownSession(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	if err := New(nil, nil, nil, ocapi.Auth{}).Abort(t.Context(), platforms.AbortRequest{SessionID: "s1", TaskID: "one"}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
}
