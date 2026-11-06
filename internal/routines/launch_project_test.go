package routines

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
)

func TestUnboundRoutineValidation(t *testing.T) {
	for _, mode := range []string{SessionNew, SessionReuse, SessionExisting} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			input := validInput()
			input.Directory, input.SessionMode, input.SessionID = "", mode, "existing"
			_, err := h.svc.Create(t.Context(), input)
			if mode == SessionNew && err != nil {
				t.Fatalf("unbound new-session routine: %v", err)
			}
			if mode != SessionNew && !errors.Is(err, ErrValidation) {
				t.Fatalf("unbound shared session must be rejected: %v", err)
			}
		})
	}
}

func TestUnboundRoutineRejectsSchedulesAndRetainedSchedule(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	input.Schedule = Schedule{Kind: ScheduleCron, Cron: "0 9 * * *", Timezone: "UTC"}
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Directory = ""
	if _, err := h.svc.Create(t.Context(), input); !errors.Is(err, ErrValidation) {
		t.Fatalf("unbound schedule: %v", err)
	}
	input.KeepSchedule = true
	if _, err := h.svc.Update(t.Context(), routine.ID, input); !errors.Is(err, ErrValidation) {
		t.Fatalf("retained schedule: %v", err)
	}
}

type projectHost struct {
	*testHost
	projects []db.ProjectStats
	err      error
}

func (h *projectHost) Projects(context.Context) ([]db.ProjectStats, error) { return h.projects, h.err }

func projectHarness(t *testing.T) (*harness, *projectHost) {
	t.Helper()
	h := newHarness(t)
	host := &projectHost{testHost: h.host, projects: []db.ProjectStats{
		{Directory: "/repo", ProjectKey: "project-one", UpstreamKeys: []string{"forge.test/org/one"}},
		{Directory: "/other", UpstreamOrigin: "forge.test/org/two"},
	}}
	h.svc.router = hostsvc.NewRouter(host)
	h.svc.projectCatalog = func(context.Context) ([]db.ProjectStats, error) { return host.projects, host.err }
	h.svc.normalizeUpstream = func(raw string) string {
		if !strings.HasPrefix(raw, "https://") {
			return ""
		}
		return strings.TrimSuffix(strings.TrimPrefix(raw, "https://"), ".git")
	}
	return h, host
}

func TestRunNowInProject(t *testing.T) {
	h, _ := projectHarness(t)
	input := validInput()
	input.Directory = ""
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RunNow(t.Context(), routine.ID); !errors.Is(err, state.ErrRoutineProject) {
		t.Fatalf("missing project: %v", err)
	}
	for i, project := range []string{"/repo", "https://forge.test/org/two.git", "project-one"} {
		h.now.Add(1)
		run, err := h.svc.RunNowInProject(t.Context(), routine.ID, project)
		want := "/repo"
		if i == 1 {
			want = "/other"
		}
		if err != nil || run.Directory != want || run.State != RunRunning {
			t.Fatalf("run = %+v, %v", run, err)
		}
		stored, err := h.db.GetRoutineRun(t.Context(), run.ID)
		if err != nil || stored.Directory != want {
			t.Fatalf("snapshot = %+v, %v", stored, err)
		}
	}
	stored, err := h.svc.Get(t.Context(), routine.ID)
	if err != nil || stored.Directory != "" || stored.SessionID != "" {
		t.Fatalf("definition changed: %+v, %v", stored, err)
	}
}

func TestLaunchProjectFailsClosed(t *testing.T) {
	h, host := projectHarness(t)
	for _, project := range []string{"", "/unknown", "https://unknown/repo", "relative"} {
		if _, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "local"}, project); err == nil {
			t.Fatalf("accepted %q", project)
		}
	}
	if _, err := h.svc.launchProject(t.Context(), state.Routine{Directory: "/repo"}, "/other"); !errors.Is(err, state.ErrRoutineProject) {
		t.Fatalf("override: %v", err)
	}
	if got, err := h.svc.launchProject(t.Context(), state.Routine{Directory: "/repo"}, ""); err != nil || got != "/repo" {
		t.Fatalf("fixed project: %q, %v", got, err)
	}
	if _, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "offline"}, "/repo"); err == nil {
		t.Fatal("disconnected owner accepted")
	}
	host.projects = append(host.projects, db.ProjectStats{Directory: "/duplicate", UpstreamKeys: []string{"forge.test/org/one"}})
	if _, err := h.svc.launchProject(t.Context(), state.Routine{}, "https://forge.test/org/one"); err == nil {
		t.Fatal("ambiguous project accepted")
	}
	host.projects[2].Archived = true
	if got, err := h.svc.launchProject(t.Context(), state.Routine{}, "https://forge.test/org/one"); err != nil || got != "/repo" {
		t.Fatalf("archived candidate: %q, %v", got, err)
	}
	host.err = errors.New("offline")
	if _, err := h.svc.launchProject(t.Context(), state.Routine{}, "/repo"); err == nil {
		t.Fatal("project read failure ignored")
	}
}

func TestLaunchProjectHonorsStoredArchiveState(t *testing.T) {
	h, host := projectHarness(t)
	if err := h.db.ArchiveProject(t.Context(), "local", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "local"}, "/repo"); err == nil {
		t.Fatal("archived project accepted")
	}
	host.projects[0].LastUsed = 1 << 62
	if directory, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "local"}, "/repo"); err != nil || directory != "/repo" {
		t.Fatalf("new activity: %q, %v", directory, err)
	}
}

func TestWebhookLaunchProject(t *testing.T) {
	h, _ := projectHarness(t)
	input := validInput()
	input.Directory = ""
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.RunWebhookInProject(t.Context(), routine.ID, "payload", "#1 opened", 1, "https://forge.test/org/one.git")
	if err != nil || run.Directory != "/repo" || run.Trigger != "webhook" {
		t.Fatalf("webhook run: %+v, %v", run, err)
	}
	if len(h.platform.sent) != 1 || !strings.HasSuffix(h.platform.sent[0].Message, "\n\npayload") {
		t.Fatalf("prompt: %+v", h.platform.sent)
	}
	input.Enabled = false
	if _, err := h.svc.Update(t.Context(), routine.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RunWebhookInProject(t.Context(), routine.ID, "", "", -2, "/repo"); err == nil {
		t.Fatal("disabled routine ran")
	}
	if _, err := h.svc.RunWebhookInProject(t.Context(), "missing", "", "", -3, "/repo"); err == nil {
		t.Fatal("missing routine ran")
	}
}

func TestRunHTTPProjectAndLegacyEmptyBody(t *testing.T) {
	h, _ := projectHarness(t)
	input := validInput()
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	mux := moduletest.Mount(&Module{svc: h.svc})
	for i, body := range []string{"", `{}`, `{"project":"/repo"}`, `{"project":"/other"}`, `{`, `{} {}`} {
		h.now.Add(1)
		r := httptest.NewRequest(http.MethodPost, "/api/routines/"+routine.ID+"/run", strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		want := http.StatusOK
		if i >= 3 {
			want = http.StatusBadRequest
		}
		if w.Code != want {
			t.Fatalf("body %q: %d: %s", body, w.Code, w.Body.String())
		}
	}
}
