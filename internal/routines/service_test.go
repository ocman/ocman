package routines

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

type testHost struct {
	hostsvc.Host
	remoteID string
	err      error
	calls    atomic.Int32
}

func (h *testHost) RemoteID() string {
	if h.remoteID != "" {
		return h.remoteID
	}
	return "local"
}
func (h *testHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.calls.Add(1)
	if h.err != nil {
		return nil, h.err
	}
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:1234", RepoRoot: "/repo", Runtime: ocruntime.Instance{ID: "test"}}, nil
}

type testPlatform struct {
	platforms.Platform
	mu           sync.Mutex
	id           platforms.ID
	status       db.SessionStatus
	sessionErr   error
	emptySession bool
	onSession    func()
	onSend       func()
	createErr    error
	sendErr      error
	created      int
	sent         []string
}

func (p *testPlatform) ID() platforms.ID {
	if p.id != "" {
		return p.id
	}
	return "opencode"
}
func (p *testPlatform) Available(context.Context) bool    { return true }
func (p *testPlatform) Owns(context.Context, string) bool { return true }
func (p *testPlatform) CreateSession(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created++
	if p.createErr != nil {
		return nil, p.createErr
	}
	return &platforms.CreateSessionResponse{ID: "session-1"}, nil
}
func (p *testPlatform) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, req.Message)
	if p.onSend != nil {
		p.onSend()
	}
	return p.sendErr
}
func (p *testPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionErr != nil {
		return nil, p.sessionErr
	}
	if p.emptySession {
		return &platforms.SessionDetail{}, nil
	}
	if p.onSession != nil {
		p.onSession()
	}
	return &platforms.SessionDetail{Session: &db.Session{ID: "session-1", Status: p.status}}, nil
}
func (p *testPlatform) setStatus(status db.SessionStatus) {
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
}
func (p *testPlatform) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.created, len(p.sent)
}

type harness struct {
	db       *state.DB
	host     *testHost
	platform *testPlatform
	svc      *Service
	now      atomic.Int64
	ids      atomic.Int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sdb, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	h := &harness{db: sdb, host: &testHost{}, platform: &testPlatform{status: db.StatusBusy}}
	h.now.Store(time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli())
	registry := platforms.NewRegistry()
	registry.Register(h.platform)
	h.svc = New(Deps{
		Store: sdb, Router: hostsvc.NewRouter(h.host), Sessions: sessionsvc.New(registry, sessionsvc.Hooks{}), Platforms: registry,
		Now: func() time.Time { return time.UnixMilli(h.now.Load()) },
		NewID: func(prefix string) string {
			return prefix + "test-" + time.UnixMilli(h.ids.Add(1)).Format("150405.000")
		},
	})
	return h
}

func validInput() Input {
	return Input{Name: "Daily check", Prompt: " inspect this\n", Directory: "/repo", Schedule: Schedule{Kind: ScheduleNone}, Enabled: true}
}

func TestValidationAndTimeoutBecomesAbsolute(t *testing.T) {
	h := newHarness(t)
	bad := []Input{
		{Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleNone}},
		{Name: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleNone}},
		{Name: "x", Prompt: "x", Directory: "relative", Schedule: Schedule{Kind: ScheduleNone}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleTimeout}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleTimeout, Timeout: -time.Second}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleOnce, At: time.UnixMilli(h.now.Load())}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleCron, Cron: "bad", Timezone: "UTC"}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleCron, Cron: "60 * * * *", Timezone: "UTC"}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: ScheduleCron, Cron: "0 9 * * *", Timezone: "Nowhere/Invalid"}},
		{Name: "x", Prompt: "x", Directory: "/repo", Schedule: Schedule{Kind: "interval"}},
	}
	for i, input := range bad {
		if _, err := h.svc.Create(t.Context(), input); !errors.Is(err, ErrValidation) {
			t.Fatalf("bad input %d: %v", i, err)
		}
	}

	input := validInput()
	input.Schedule = Schedule{Kind: ScheduleTimeout, Timeout: 5 * time.Minute}
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	want := h.now.Load() + (5 * time.Minute).Milliseconds()
	if routine.NextDueAt != want || routine.ScheduleConfigJSON != `{"dueAt":1893488700000}` {
		t.Fatalf("routine = %+v, want due %d", routine, want)
	}
	input.Name = "Cron default timezone"
	input.Schedule = Schedule{Kind: ScheduleCron, Cron: "0 9 * * *"}
	routine, err = h.svc.Create(t.Context(), input)
	if err != nil || !strings.Contains(routine.ScheduleConfigJSON, `"timezone":"UTC"`) {
		t.Fatalf("cron routine = %+v, %v", routine, err)
	}
}

func TestCRUD(t *testing.T) {
	h := newHarness(t)
	routine, err := h.svc.Create(t.Context(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	input := validInput()
	input.Name, input.Prompt = "Renamed", "new prompt"
	updated, err := h.svc.Update(t.Context(), routine.ID, input)
	if err != nil || updated.Name != "Renamed" || updated.Prompt != "new prompt" {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	listed, err := h.svc.List(t.Context(), false)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %+v, %v", listed, err)
	}
	if err := h.svc.Delete(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}
	if listed, err = h.svc.List(t.Context(), false); err != nil || len(listed) != 0 {
		t.Fatalf("List after delete = %+v, %v", listed, err)
	}
}

func TestDefaultsConflictsAndMissingOperations(t *testing.T) {
	sdb, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	svc := New(Deps{Store: sdb})

	routine, err := svc.Create(t.Context(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(routine.ID) != len("routine-")+16 || routine.RemoteID != "local" || routine.CreatedAt == 0 {
		t.Fatalf("defaults = %+v", routine)
	}
	if got, err := svc.Get(t.Context(), routine.ID); err != nil || got.ID != routine.ID {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if history, err := svc.History(t.Context(), routine.ID); err != nil || len(history) != 0 {
		t.Fatalf("History = %+v, %v", history, err)
	}
	if _, err := svc.Create(t.Context(), validInput()); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("duplicate Create error = %v", err)
	}
	if _, err := svc.Update(t.Context(), "missing", validInput()); !errors.Is(err, state.ErrRoutineNotFound) {
		t.Fatalf("missing Update error = %v", err)
	}
	if _, err := svc.Update(t.Context(), routine.ID, Input{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid Update error = %v", err)
	}
	if _, err := svc.History(t.Context(), "missing"); !errors.Is(err, state.ErrRoutineNotFound) {
		t.Fatalf("missing History error = %v", err)
	}
}

func TestManualAndDueDispatchShareAtomicClaim(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	input.Schedule = Schedule{Kind: ScheduleOnce, At: time.UnixMilli(h.now.Load()).Add(time.Minute)}
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	h.now.Store(routine.NextDueAt)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, run := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := h.svc.RunNow(ctx, routine.ID); return err },
		h.svc.Tick,
	} {
		wg.Add(1)
		go func(run func(context.Context) error) { defer wg.Done(); <-start; _ = run(t.Context()) }(run)
	}
	close(start)
	wg.Wait()
	created, sent := h.platform.counts()
	if created != 1 || sent != 1 || h.host.calls.Load() != 1 {
		t.Fatalf("ensure=%d created=%d sent=%d", h.host.calls.Load(), created, sent)
	}
	runs, err := h.db.ListRoutineRuns(t.Context(), routine.ID)
	if err != nil || len(runs) != 1 || runs[0].State != RunRunning {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
}

func TestDispatchFailuresPersist(t *testing.T) {
	for name, setup := range map[string]func(*harness){
		"ensure": func(h *harness) { h.host.err = errors.New("ensure failed") },
		"create": func(h *harness) { h.platform.createErr = errors.New("create failed") },
		"send":   func(h *harness) { h.platform.sendErr = errors.New("send failed") },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			setup(h)
			routine, _ := h.svc.Create(t.Context(), validInput())
			run, err := h.svc.RunNow(t.Context(), routine.ID)
			if err != nil || run.State != RunFailure || run.Error == "" || run.FinishedAt == 0 {
				t.Fatalf("RunNow = %+v, %v", run, err)
			}
		})
	}
}

func TestUnavailableTargetFailsRun(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	input.RemoteID = "missing"
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.RunNow(t.Context(), routine.ID)
	if err != nil || run.State != RunFailure || !strings.Contains(run.Error, "unavailable") {
		t.Fatalf("RunNow = %+v, %v", run, err)
	}
}

func TestRemoteDispatchUsesCompoundPlatform(t *testing.T) {
	h := newHarness(t)
	remoteHost := &testHost{remoteID: "remote"}
	remotePlatform := &testPlatform{id: "r-remote:opencode", status: db.StatusBusy}
	h.svc.router.RegisterRemote("remote", remoteHost)
	h.svc.platforms.Register(remotePlatform)
	input := validInput()
	input.RemoteID = "remote"
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.RunNow(t.Context(), routine.ID)
	if err != nil || run.Platform != "r-remote:opencode" || run.SessionID != "session-1" {
		t.Fatalf("RunNow = %+v, %v", run, err)
	}
}

func TestStoreLossDuringDispatchIsReported(t *testing.T) {
	h := newHarness(t)
	h.platform.onSend = func() { _ = h.db.Close() }
	routine, _ := h.svc.Create(t.Context(), validInput())
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err == nil || !strings.Contains(err.Error(), "recording routine failure") {
		t.Fatalf("RunNow error = %v", err)
	}
}

func TestStoreLossDuringTickIsReported(t *testing.T) {
	h := newHarness(t)
	routine, _ := h.svc.Create(t.Context(), validInput())
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}
	h.platform.onSession = func() { _ = h.db.Close() }
	if err := h.svc.Tick(t.Context()); err == nil {
		t.Fatal("Tick succeeded after the store closed")
	}
}

func TestRecoverySkipsUnavailablePlatform(t *testing.T) {
	h := newHarness(t)
	routine, _ := h.svc.Create(t.Context(), validInput())
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}
	h.svc.platforms = platforms.NewRegistry()
	if err := h.svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	runs, err := h.db.ListRoutineRuns(t.Context(), routine.ID)
	if err != nil || len(runs) != 1 || runs[0].State != RunRunning {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
}

func TestRecoverySkipsUnavailableSession(t *testing.T) {
	for _, setup := range []func(*testPlatform){
		func(p *testPlatform) { p.sessionErr = errors.New("session unavailable") },
		func(p *testPlatform) { p.emptySession = true },
	} {
		h := newHarness(t)
		routine, _ := h.svc.Create(t.Context(), validInput())
		if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
			t.Fatal(err)
		}
		setup(h.platform)
		if err := h.svc.Recover(t.Context()); err != nil {
			t.Fatal(err)
		}
		runs, _ := h.db.ListRoutineRuns(t.Context(), routine.ID)
		if runs[0].State != RunRunning {
			t.Fatalf("run = %+v", runs[0])
		}
	}
}

func TestRecoveryFailsUnlinkedRun(t *testing.T) {
	h := newHarness(t)
	routine, _ := h.svc.Create(t.Context(), validInput())
	run, claimed, err := h.db.ClaimRoutineRun(t.Context(), state.RoutineRun{
		ID: "run-orphan", RoutineID: routine.ID, Trigger: "schedule", State: RunRunning, OccurrenceAt: 1, CreatedAt: 1,
	})
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %v, %v", run, claimed, err)
	}
	if err := h.svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := h.db.GetRoutineRun(t.Context(), run.ID)
	if err != nil || got.State != RunFailure || !strings.Contains(got.Error, "before session linkage") {
		t.Fatalf("run = %+v, %v", got, err)
	}
}

func TestMalformedCronSnapshotPreventsCompletion(t *testing.T) {
	for _, config := range []string{"{", `{"cron":"bad cron value","timezone":"UTC"}`} {
		h := newHarness(t)
		input := validInput()
		input.Schedule = Schedule{Kind: ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"}
		routine, err := h.svc.Create(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
			t.Fatal(err)
		}
		routine.ScheduleConfigJSON = config
		if err := h.db.UpdateRoutine(t.Context(), routine); err != nil {
			t.Fatal(err)
		}
		h.platform.setStatus(db.StatusDone)
		if err := h.svc.Tick(t.Context()); err == nil {
			t.Fatal("Tick completed a run with malformed cron configuration")
		}
		runs, _ := h.db.ListRoutineRuns(t.Context(), routine.ID)
		if runs[0].State != RunRunning {
			t.Fatalf("run = %+v", runs[0])
		}
	}
}

func TestTerminalStatusesRecurrenceOneShotsAndAutoDelete(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     db.SessionStatus
		wantState  string
		schedule   Schedule
		autoDelete bool
		wantNext   bool
	}{
		{"done cron", db.StatusDone, RunSuccess, Schedule{Kind: ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"}, false, true},
		{"error", db.StatusError, RunFailure, Schedule{Kind: ScheduleNone}, false, false},
		{"interrupted", db.StatusInterrupted, RunFailure, Schedule{Kind: ScheduleNone}, false, false},
		{"once", db.StatusDone, RunSuccess, Schedule{Kind: ScheduleOnce, At: time.Date(2030, 1, 1, 9, 1, 0, 0, time.UTC)}, false, false},
		{"timeout", db.StatusDone, RunSuccess, Schedule{Kind: ScheduleTimeout, Timeout: time.Minute}, false, false},
		{"auto delete", db.StatusDone, RunSuccess, Schedule{Kind: ScheduleNone}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			input := validInput()
			input.Schedule, input.DeleteAfterSuccess = tc.schedule, tc.autoDelete
			routine, err := h.svc.Create(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
				t.Fatal(err)
			}
			h.platform.setStatus(tc.status)
			h.now.Add(time.Minute.Milliseconds())
			if err := h.svc.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			runs, _ := h.db.ListRoutineRuns(t.Context(), routine.ID)
			got, _ := h.db.GetRoutine(t.Context(), routine.ID)
			if runs[0].State != tc.wantState || runs[0].FinishedAt == 0 || (got.NextDueAt > h.now.Load()) != tc.wantNext || got.Deleted != tc.autoDelete {
				t.Fatalf("run=%+v routine=%+v", runs[0], got)
			}
		})
	}
}

func TestFinishingRunDoesNotOverwriteRoutineEditedAfterClaim(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	input.Schedule = Schedule{Kind: ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"}
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}

	h.now.Add(time.Minute.Milliseconds())
	edited := validInput()
	edited.Schedule = Schedule{Kind: ScheduleCron, Cron: "0 12 * * *", Timezone: "UTC"}
	editedRoutine, err := h.svc.Update(t.Context(), routine.ID, edited)
	if err != nil {
		t.Fatal(err)
	}
	h.platform.setStatus(db.StatusDone)
	h.now.Add(time.Minute.Milliseconds())
	if err := h.svc.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}

	got, err := h.db.GetRoutine(t.Context(), routine.ID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := h.db.ListRoutineRuns(t.Context(), routine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].State != RunSuccess {
		t.Fatalf("run = %+v", runs[0])
	}
	if got.ScheduleConfigJSON != editedRoutine.ScheduleConfigJSON || got.NextDueAt != editedRoutine.NextDueAt || got.UpdatedAt != editedRoutine.UpdatedAt {
		t.Fatalf("routine = %+v, want edited schedule %+v", got, editedRoutine)
	}
}

func TestRecoveryKeepsBusyLinkedRunAndSettlesItLater(t *testing.T) {
	h := newHarness(t)
	routine, _ := h.svc.Create(t.Context(), validInput())
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	runs, _ := h.db.ListRoutineRuns(t.Context(), routine.ID)
	if runs[0].State != RunRunning {
		t.Fatalf("busy run = %+v", runs[0])
	}
	h.platform.setStatus(db.StatusWaiting)
	if err := h.svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	runs, _ = h.db.ListRoutineRuns(t.Context(), routine.ID)
	if runs[0].State != RunRunning {
		t.Fatalf("waiting run = %+v", runs[0])
	}
	h.platform.setStatus(db.StatusDone)
	if err := h.svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	runs, _ = h.db.ListRoutineRuns(t.Context(), routine.ID)
	if runs[0].State != RunSuccess {
		t.Fatalf("settled run = %+v", runs[0])
	}
}
