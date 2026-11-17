package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/srvtiming"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestRequestFailuresVisibleWithoutDebug(t *testing.T) {
	oldHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	oldLevel := log.GetLevel()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(oldHooks); log.SetLevel(oldLevel) })
	hook := logtest.NewGlobal()
	log.SetLevel(log.InfoLevel)
	for _, code := range []int{500, 502, 503, 504} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			hook.Reset()
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/session/{id}/send", func(w http.ResponseWriter, r *http.Request) {
				srvtiming.Record(r.Context(), "upstream", 20*time.Millisecond, "")
				w.WriteHeader(code)
				w.WriteHeader(http.StatusOK) // log the committed status, not the last attempted one
			})
			req := httptest.NewRequest(http.MethodPost, "/api/session/s1/send?token=SECRET", strings.NewReader("SECRET body"))
			req.Header.Set("Authorization", "Bearer SECRET")
			rr := httptest.NewRecorder()
			withRequestTiming(mux).ServeHTTP(rr, req)
			entries := hook.AllEntries()
			if len(entries) != 1 {
				t.Fatalf("want one default-level failure log, got %v", entries)
			}
			e := entries[0]
			if e.Level != log.WarnLevel || e.Data["status"] != code || e.Data["method"] != "POST" || e.Data["path"] != "/api/session/s1/send" || e.Data["route"] != "POST /api/session/{id}/send" || e.Data["timings"] == nil || e.Data["duration_ms"] == nil {
				t.Errorf("incomplete failure context: %+v", e)
			}
			text, err := e.String()
			if err != nil || strings.Contains(text, "SECRET") {
				t.Errorf("request secrets logged: %q %v", text, err)
			}
			if rr.Code != code {
				t.Errorf("response changed: %d", rr.Code)
			}
		})
	}
}

func TestExpectedRequestStatusesStayQuiet(t *testing.T) {
	oldHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	oldLevel := log.GetLevel()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(oldHooks); log.SetLevel(oldLevel) })
	hook := logtest.NewGlobal()
	log.SetLevel(log.InfoLevel)
	for _, code := range []int{200, 304, 400, 401, 403, 404, 409, 429, 499} {
		withRequestTiming(ringHandler(0, code)).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	}
	if entries := hook.AllEntries(); len(entries) != 0 {
		t.Fatalf("routine requests produced operator noise: %v", entries)
	}
}
