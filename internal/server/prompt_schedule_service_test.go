package server

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type fakeStore struct {
	mu                             sync.Mutex
	schedules                      map[string]state.PromptSchedule
	createErr, listErr, recoverErr error
}

func newFakeStore() *fakeStore { return &fakeStore{schedules: map[string]state.PromptSchedule{}} }

func (f *fakeStore) CreatePromptSchedule(_ context.Context, schedule state.PromptSchedule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.schedules[schedule.ID] = schedule
	return nil
}

func (f *fakeStore) ListPromptSchedules(_ context.Context, directory, remoteID string) ([]state.PromptSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []state.PromptSchedule
	for _, schedule := range f.schedules {
		if schedule.Directory == directory && schedule.RemoteID == remoteID {
			out = append(out, schedule)
		}
	}
	return out, nil
}

func (f *fakeStore) ListRunningPromptSchedules(context.Context) ([]state.PromptSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recoverErr != nil {
		return nil, f.recoverErr
	}
	var out []state.PromptSchedule
	for _, schedule := range f.schedules {
		if schedule.State == StateRunning {
			out = append(out, schedule)
		}
	}
	return out, nil
}

func (f *fakeStore) GetPromptSchedule(_ context.Context, id string) (state.PromptSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok {
		return schedule, state.ErrPromptScheduleNotFound
	}
	return schedule, nil
}

func (f *fakeStore) ClaimPromptSchedule(_ context.Context, id string, now int64, force bool) (state.PromptSchedule, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok || schedule.State != StateScheduled || (!force && (!schedule.Enabled || schedule.RunAt > now)) {
		return schedule, false, nil
	}
	schedule.State, schedule.StartedAt, schedule.UpdatedAt = StateRunning, now, now
	f.schedules[id] = schedule
	return schedule, true, nil
}

func (f *fakeStore) ClaimNextDuePromptSchedule(_ context.Context, now int64) (state.PromptSchedule, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, schedule := range f.schedules {
		if schedule.State == StateScheduled && schedule.Enabled && schedule.RunAt <= now {
			schedule.State, schedule.StartedAt, schedule.UpdatedAt = StateRunning, now, now
			f.schedules[id] = schedule
			return schedule, true, nil
		}
	}
	return state.PromptSchedule{}, false, nil
}

func (f *fakeStore) CancelPromptSchedule(_ context.Context, id string, now int64) (state.PromptSchedule, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok || (schedule.State != StateScheduled && schedule.State != StateRunning) {
		return schedule, false, nil
	}
	if schedule.State == StateRunning {
		schedule.FinishedAt = now
	}
	schedule.State, schedule.UpdatedAt = StateCanceled, now
	f.schedules[id] = schedule
	return schedule, true, nil
}

func (f *fakeStore) LinkPromptScheduleSession(_ context.Context, id, platform, sessionID string, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule := f.schedules[id]
	if schedule.State != StateRunning {
		return state.ErrPromptScheduleSuperseded
	}
	schedule.Platform, schedule.SessionID, schedule.UpdatedAt = platform, sessionID, now
	f.schedules[id] = schedule
	return nil
}

func (f *fakeStore) FinishPromptSchedule(_ context.Context, id, stateValue, errorText string, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule := f.schedules[id]
	if schedule.State != StateRunning {
		return state.ErrPromptScheduleSuperseded
	}
	schedule.State, schedule.Error, schedule.FinishedAt, schedule.UpdatedAt = stateValue, errorText, now, now
	f.schedules[id] = schedule
	return nil
}

func (f *fakeStore) CompletePromptSchedule(_ context.Context, id string, nextRunAt, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule := f.schedules[id]
	if schedule.State != StateRunning {
		return state.ErrPromptScheduleSuperseded
	}
	schedule.State, schedule.RunAt, schedule.Error, schedule.FinishedAt, schedule.UpdatedAt = StateScheduled, nextRunAt, "", now, now
	f.schedules[id] = schedule
	return nil
}

func (f *fakeStore) ReschedulePromptSchedule(_ context.Context, id string, nextRunAt int64, errorText string, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule := f.schedules[id]
	if schedule.State != StateRunning {
		return state.ErrPromptScheduleSuperseded
	}
	schedule.State, schedule.RunAt, schedule.Error, schedule.FinishedAt, schedule.UpdatedAt = StateScheduled, nextRunAt, errorText, now, now
	f.schedules[id] = schedule
	return nil
}

func (f *fakeStore) SetPromptScheduleEnabled(_ context.Context, id string, enabled bool, runAt, now int64) (state.PromptSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok {
		return schedule, state.ErrPromptScheduleNotFound
	}
	schedule.Enabled, schedule.RunAt, schedule.UpdatedAt = enabled, runAt, now
	if enabled {
		schedule.State, schedule.Error = StateScheduled, ""
	} else if schedule.State == StateRunning {
		schedule.State, schedule.FinishedAt = StateScheduled, now
	}
	f.schedules[id] = schedule
	return schedule, nil
}

func (f *fakeStore) FailRunningPromptSchedules(_ context.Context, now int64, errorText string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recoverErr != nil {
		return f.recoverErr
	}
	for id, schedule := range f.schedules {
		if schedule.State == StateRunning {
			schedule.State, schedule.Error, schedule.FinishedAt, schedule.UpdatedAt = StateFailed, errorText, now, now
			f.schedules[id] = schedule
		}
	}
	return nil
}

type fakeSessions struct {
	created []platforms.CreateSessionRequest
	sent    []platforms.SendMessageRequest
	queued  []bool
	create  error
	send    error
}

func (f *fakeSessions) CreateScheduledSession(_ context.Context, _ string, directory string) (string, *platforms.CreateSessionResponse, error) {
	f.created = append(f.created, platforms.CreateSessionRequest{Directory: directory})
	if f.create != nil {
		return "", nil, f.create
	}
	return "opencode", &platforms.CreateSessionResponse{ID: "session-1"}, nil
}

func (f *fakeSessions) SendScheduledMessage(_ context.Context, _ string, req platforms.SendMessageRequest, queue bool) error {
	f.sent = append(f.sent, req)
	f.queued = append(f.queued, queue)
	return f.send
}

func testPromptScheduleService(store Store, sessions Sessions) *promptScheduleService {
	return newPromptScheduleService(store, sessions, func() time.Time { return time.UnixMilli(2000) }, func() string { return "schedule-1" })
}

func TestCreateListInspectAndCancel(t *testing.T) {
	store := newFakeStore()
	svc := testPromptScheduleService(store, &fakeSessions{})
	schedule, err := svc.Create(context.Background(), promptScheduleCreateRequest{Directory: "/repo", Prompt: " unchanged \n", RunAt: 3000})
	if err != nil || schedule.Prompt != " unchanged \n" || schedule.State != StateScheduled {
		t.Fatalf("Create: schedule=%+v err=%v", schedule, err)
	}
	listed, err := svc.List(context.Background(), "/repo", "local")
	if err != nil || len(listed) != 1 {
		t.Fatalf("List: schedules=%+v err=%v", listed, err)
	}
	inspected, err := svc.Get(context.Background(), schedule.ID)
	if err != nil || inspected.ID != schedule.ID {
		t.Fatalf("Get: schedule=%+v err=%v", inspected, err)
	}
	canceled, err := svc.Cancel(context.Background(), schedule.ID)
	if err != nil || canceled.State != StateCanceled {
		t.Fatalf("Cancel: schedule=%+v err=%v", canceled, err)
	}
}

func TestTickCreatesOneSessionAndSendsPromptUnchanged(t *testing.T) {
	store := newFakeStore()
	sessions := &fakeSessions{}
	now := time.UnixMilli(500)
	svc := newPromptScheduleService(store, sessions, func() time.Time { return now }, func() string { return "schedule-1" })
	created, _ := svc.Create(context.Background(), promptScheduleCreateRequest{Directory: "/repo", Prompt: " exact\n", RunAt: 1000})
	now = time.UnixMilli(2000)

	if err := svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(context.Background(), created.ID)
	if len(sessions.created) != 1 || len(sessions.sent) != 1 || sessions.sent[0].Message != " exact\n" {
		t.Fatalf("created=%d sent=%+v", len(sessions.created), sessions.sent)
	}
	if got.State != StateCompleted || got.SessionID != "session-1" || got.Platform != "opencode" {
		t.Fatalf("schedule=%+v", got)
	}
}

func TestRecurringFreshAndReuseSessions(t *testing.T) {
	for _, mode := range []string{SessionModeFresh, SessionModeReuse} {
		t.Run(mode, func(t *testing.T) {
			store := newFakeStore()
			sessions := &fakeSessions{}
			now := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
			svc := newPromptScheduleService(store, sessions, func() time.Time { return now }, func() string { return "schedule-1" })
			created, err := svc.Create(t.Context(), promptScheduleCreateRequest{
				Directory: "/repo", Prompt: "go", TimingType: TimingInterval, IntervalMinutes: 10,
				Timezone: "Europe/Brussels", SessionMode: mode,
			})
			if err != nil || created.RunAt != now.Add(10*time.Minute).UnixMilli() || !created.Enabled {
				t.Fatalf("Create: schedule=%+v err=%v", created, err)
			}

			now = now.Add(10 * time.Minute)
			if err := svc.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(10 * time.Minute)
			if err := svc.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			wantCreated := 2
			if mode == SessionModeReuse {
				wantCreated = 1
			}
			got, _ := svc.Get(t.Context(), created.ID)
			if len(sessions.created) != wantCreated || len(sessions.sent) != 2 || got.State != StateScheduled || got.RunAt != now.Add(10*time.Minute).UnixMilli() {
				t.Fatalf("created=%d sent=%d schedule=%+v", len(sessions.created), len(sessions.sent), got)
			}
			if mode == SessionModeReuse && (sessions.queued[0] || !sessions.queued[1]) {
				t.Fatalf("reuse queue decisions=%v", sessions.queued)
			}
		})
	}
}

func TestCronTimezoneAndDisabledSchedule(t *testing.T) {
	store := newFakeStore()
	sessions := &fakeSessions{}
	now := time.Date(2030, 1, 1, 8, 30, 0, 0, time.UTC)
	svc := newPromptScheduleService(store, sessions, func() time.Time { return now }, func() string { return "schedule-1" })
	created, err := svc.Create(t.Context(), promptScheduleCreateRequest{
		Directory: "/repo", Prompt: "go", TimingType: TimingCron, Cron: "0 9 * * *", Timezone: "Europe/Brussels", SessionMode: SessionModeFresh,
	})
	if err != nil || created.RunAt != time.Date(2030, 1, 2, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("Create: schedule=%+v err=%v", created, err)
	}
	if _, err := svc.SetEnabled(t.Context(), created.ID, false); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2030, 1, 2, 9, 0, 0, 0, time.UTC)
	if err := svc.Tick(t.Context()); err != nil || len(sessions.created) != 0 {
		t.Fatalf("Tick: created=%d err=%v", len(sessions.created), err)
	}
	got, err := svc.SetEnabled(t.Context(), created.ID, true)
	if err != nil || !got.Enabled || got.RunAt <= now.UnixMilli() {
		t.Fatalf("SetEnabled: schedule=%+v err=%v", got, err)
	}
}

func TestCreateRejectsUnsafeRecurringTiming(t *testing.T) {
	for name, req := range map[string]promptScheduleCreateRequest{
		"overflowing interval": {Directory: "/repo", Prompt: "go", TimingType: TimingInterval, IntervalMinutes: math.MaxInt64},
		"embedded timezone":    {Directory: "/repo", Prompt: "go", TimingType: TimingCron, Cron: "CRON_TZ=UTC 0 9 * * *", Timezone: "Europe/Brussels"},
		"descriptor":           {Directory: "/repo", Prompt: "go", TimingType: TimingCron, Cron: "@every 5m", Timezone: "UTC"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPromptScheduleService(newFakeStore(), &fakeSessions{}).Create(t.Context(), req)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("Create error = %v, want validation error", err)
			}
		})
	}
}

func TestConcurrentTicksDispatchOnce(t *testing.T) {
	store := newFakeStore()
	sessions := &fakeSessions{}
	now := time.UnixMilli(500)
	svc := newPromptScheduleService(store, sessions, func() time.Time { return now }, func() string { return "schedule-1" })
	_, _ = svc.Create(context.Background(), promptScheduleCreateRequest{Directory: "/repo", Prompt: "go", RunAt: 1000})
	now = time.UnixMilli(2000)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = svc.Tick(context.Background()) }()
	}
	wg.Wait()
	if len(sessions.created) != 1 {
		t.Fatalf("created %d sessions", len(sessions.created))
	}
}

func TestRunNowAndErrorsPersist(t *testing.T) {
	store := newFakeStore()
	sessions := &fakeSessions{send: errors.New("send failed")}
	svc := testPromptScheduleService(store, sessions)
	created, _ := svc.Create(context.Background(), promptScheduleCreateRequest{Directory: "/repo", Prompt: "go", RunAt: 9000})
	got, err := svc.RunNow(context.Background(), created.ID)
	if err != nil || got.State != StateFailed || got.Error != "send failed" || got.SessionID != "session-1" {
		t.Fatalf("RunNow: schedule=%+v err=%v", got, err)
	}
}

func TestRecurringDispatchFailuresReschedule(t *testing.T) {
	for name, sessions := range map[string]*fakeSessions{
		"create": {create: errors.New("create failed")},
		"send":   {send: errors.New("send failed")},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			store.schedules["recurring"] = state.PromptSchedule{ID: "recurring", Directory: "/repo", Prompt: "go", RunAt: 1000, State: StateScheduled, TimingType: TimingInterval, IntervalMinutes: 10, Timezone: "UTC", Enabled: true, SessionMode: SessionModeFresh}
			if err := testPromptScheduleService(store, sessions).Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			got := store.schedules["recurring"]
			if got.State != StateScheduled || got.RunAt != time.UnixMilli(2000).Add(10*time.Minute).UnixMilli() || got.Error == "" {
				t.Fatalf("schedule=%+v", got)
			}
		})
	}
}

func TestOnceDispatchFailureStaysFailed(t *testing.T) {
	store := newFakeStore()
	store.schedules["once"] = state.PromptSchedule{ID: "once", Directory: "/repo", Prompt: "go", RunAt: 1000, State: StateScheduled, TimingType: TimingOnce, Timezone: "UTC", Enabled: true, SessionMode: SessionModeFresh}
	if err := testPromptScheduleService(store, &fakeSessions{create: errors.New("create failed")}).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := store.schedules["once"]; got.State != StateFailed || got.Error != "create failed" {
		t.Fatalf("schedule=%+v", got)
	}
}

func TestCanceledDispatchLeavesScheduleRunning(t *testing.T) {
	store := newFakeStore()
	store.schedules["canceled"] = state.PromptSchedule{ID: "canceled", Directory: "/repo", Prompt: "go", RunAt: 1000, State: StateScheduled, TimingType: TimingInterval, IntervalMinutes: 10, Timezone: "UTC", Enabled: true, SessionMode: SessionModeFresh}
	err := testPromptScheduleService(store, &fakeSessions{create: context.Canceled}).Tick(t.Context())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Tick error = %v", err)
	}
	if got := store.schedules["canceled"]; got.State != StateRunning || got.Error != "" {
		t.Fatalf("schedule=%+v", got)
	}
}

func TestValidationAndInvalidTransitions(t *testing.T) {
	svc := testPromptScheduleService(newFakeStore(), &fakeSessions{})
	for _, req := range []promptScheduleCreateRequest{{Prompt: "x", RunAt: 3000}, {Directory: "/repo", RunAt: 3000}, {Directory: "/repo", Prompt: "x", RunAt: 2000}} {
		if _, err := svc.Create(context.Background(), req); err == nil {
			t.Fatalf("Create(%+v) succeeded", req)
		}
	}
	created, _ := svc.Create(context.Background(), promptScheduleCreateRequest{Directory: "/repo", Prompt: "x", RunAt: 3000})
	_, _ = svc.Cancel(context.Background(), created.ID)
	if _, err := svc.RunNow(context.Background(), created.ID); err == nil {
		t.Fatal("RunNow canceled schedule succeeded")
	}
}

func TestRecoverMarksInterruptedDispatchFailedWithoutReplaying(t *testing.T) {
	store := newFakeStore()
	store.schedules["interrupted"] = state.PromptSchedule{ID: "interrupted", State: StateRunning}
	sessions := &fakeSessions{}
	svc := testPromptScheduleService(store, sessions)
	if err := svc.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(context.Background(), "interrupted")
	if got.State != StateFailed || got.Error == "" || len(sessions.created) != 0 {
		t.Fatalf("schedule=%+v created=%d", got, len(sessions.created))
	}
}

func TestRecoverPreservesValidRecurringSchedules(t *testing.T) {
	store := newFakeStore()
	for _, schedule := range []state.PromptSchedule{
		{ID: "once", State: StateRunning, TimingType: TimingOnce, Timezone: "UTC"},
		{ID: "interval", State: StateRunning, TimingType: TimingInterval, IntervalMinutes: 10, Timezone: "UTC"},
		{ID: "cron", State: StateRunning, TimingType: TimingCron, Cron: "*/5 * * * *", Timezone: "UTC"},
		{ID: "corrupt", State: StateRunning, TimingType: TimingInterval, IntervalMinutes: 0, Timezone: "UTC"},
	} {
		store.schedules[schedule.ID] = schedule
	}

	if err := testPromptScheduleService(store, &fakeSessions{}).Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := store.schedules["once"]; got.State != StateFailed || got.Error == "" {
		t.Fatalf("once=%+v", got)
	}
	for _, id := range []string{"interval", "cron"} {
		if got := store.schedules[id]; got.State != StateScheduled || got.RunAt <= 2000 || got.Error == "" {
			t.Fatalf("%s=%+v", id, got)
		}
	}
	if got := store.schedules["corrupt"]; got.State != StateFailed || got.Error == "" {
		t.Fatalf("corrupt=%+v", got)
	}
}

func TestRecoverReturnsStoreError(t *testing.T) {
	want := errors.New("locked")
	svc := testPromptScheduleService(&fakeStore{schedules: map[string]state.PromptSchedule{}, recoverErr: want}, &fakeSessions{})
	if err := svc.Recover(t.Context()); !errors.Is(err, want) {
		t.Fatalf("Recover error = %v", err)
	}
}
