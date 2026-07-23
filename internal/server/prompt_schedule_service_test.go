package server

import (
	"context"
	"errors"
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

func (f *fakeStore) CreatePromptSchedule(schedule state.PromptSchedule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.schedules[schedule.ID] = schedule
	return nil
}

func (f *fakeStore) ListPromptSchedules(directory, remoteID string) ([]state.PromptSchedule, error) {
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

func (f *fakeStore) GetPromptSchedule(id string) (state.PromptSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok {
		return schedule, state.ErrPromptScheduleNotFound
	}
	return schedule, nil
}

func (f *fakeStore) ClaimPromptSchedule(id string, now int64, force bool) (state.PromptSchedule, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok || schedule.State != StateScheduled || (!force && schedule.RunAt > now) {
		return schedule, false, nil
	}
	schedule.State, schedule.StartedAt, schedule.UpdatedAt = StateRunning, now, now
	f.schedules[id] = schedule
	return schedule, true, nil
}

func (f *fakeStore) ClaimNextDuePromptSchedule(now int64) (state.PromptSchedule, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, schedule := range f.schedules {
		if schedule.State == StateScheduled && schedule.RunAt <= now {
			schedule.State, schedule.StartedAt, schedule.UpdatedAt = StateRunning, now, now
			f.schedules[id] = schedule
			return schedule, true, nil
		}
	}
	return state.PromptSchedule{}, false, nil
}

func (f *fakeStore) CancelPromptSchedule(id string, now int64) (state.PromptSchedule, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule, ok := f.schedules[id]
	if !ok || schedule.State != StateScheduled {
		return schedule, false, nil
	}
	schedule.State, schedule.UpdatedAt = StateCanceled, now
	f.schedules[id] = schedule
	return schedule, true, nil
}

func (f *fakeStore) LinkPromptScheduleSession(id, platform, sessionID string, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule := f.schedules[id]
	schedule.Platform, schedule.SessionID, schedule.UpdatedAt = platform, sessionID, now
	f.schedules[id] = schedule
	return nil
}

func (f *fakeStore) FinishPromptSchedule(id, stateValue, errorText string, now int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	schedule := f.schedules[id]
	schedule.State, schedule.Error, schedule.FinishedAt, schedule.UpdatedAt = stateValue, errorText, now, now
	f.schedules[id] = schedule
	return nil
}

func (f *fakeStore) FailRunningPromptSchedules(now int64, errorText string) error {
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

func (f *fakeSessions) SendScheduledMessage(_ context.Context, _ string, req platforms.SendMessageRequest) error {
	f.sent = append(f.sent, req)
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
	if err := svc.Recover(); err != nil {
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

func TestRecoverReturnsStoreError(t *testing.T) {
	want := errors.New("locked")
	svc := testPromptScheduleService(&fakeStore{schedules: map[string]state.PromptSchedule{}, recoverErr: want}, &fakeSessions{})
	if err := svc.Recover(); !errors.Is(err, want) {
		t.Fatalf("Recover error = %v", err)
	}
}
