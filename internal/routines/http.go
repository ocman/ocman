package routines

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineRequest struct {
	Platform                   string                     `json:"platform"`
	Name                       string                     `json:"name"`
	Prompt                     string                     `json:"prompt"`
	Directory                  string                     `json:"directory"`
	RemoteID                   string                     `json:"remoteId"`
	Agent                      string                     `json:"agent"`
	Model                      string                     `json:"model"`
	SessionMode                string                     `json:"sessionMode"`
	SessionID                  string                     `json:"sessionId"`
	Worktree                   bool                       `json:"worktree"`
	CleanupWorktree            bool                       `json:"cleanupWorktree"`
	Schedule                   routineScheduleRequest     `json:"schedule"`
	Enabled                    bool                       `json:"enabled"`
	DeleteAfterSuccess         bool                       `json:"deleteAfterSuccess"`
	ArchiveSessionAfterSuccess bool                       `json:"archiveSessionAfterSuccess"`
	NotifyOnSuccess            bool                       `json:"notifyOnSuccess"`
	PermissionRules            []platforms.PermissionRule `json:"permissionRules"`
}

type routineScheduleRequest struct {
	Kind      string `json:"kind"`
	TimeoutMS int64  `json:"timeoutMs"`
	At        int64  `json:"at"`
	Cron      string `json:"cron"`
	Timezone  string `json:"timezone"`
}

func (req routineRequest) input() (Input, error) {
	if req.Schedule.TimeoutMS > math.MaxInt64/int64(time.Millisecond) || req.Schedule.TimeoutMS < math.MinInt64/int64(time.Millisecond) {
		return Input{}, ErrValidation
	}
	return Input{
		Platform: req.Platform,
		Name:     req.Name, Prompt: req.Prompt, Directory: req.Directory, RemoteID: req.RemoteID, Agent: req.Agent, Model: req.Model, SessionMode: req.SessionMode, SessionID: req.SessionID,
		Worktree: req.Worktree, CleanupWorktree: req.CleanupWorktree,
		Schedule: Schedule{
			Kind: req.Schedule.Kind, Timeout: time.Duration(req.Schedule.TimeoutMS) * time.Millisecond,
			At: time.UnixMilli(req.Schedule.At), Cron: req.Schedule.Cron, Timezone: req.Schedule.Timezone,
		},
		Enabled: req.Enabled, DeleteAfterSuccess: req.DeleteAfterSuccess,
		ArchiveSessionAfterSuccess: req.ArchiveSessionAfterSuccess,
		NotifyOnSuccess:            req.NotifyOnSuccess,
		PermissionRules:            req.PermissionRules,
	}, nil
}

func (m *Module) handleRun(w http.ResponseWriter, r *http.Request) {
	run, err := m.svc.RunNow(r.Context(), r.PathValue("id"))
	if err != nil {
		writeHTTPError(w, "running routine", err)
		return
	}
	httpx.WriteJSON(w, run)
}

func (m *Module) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit, before, ok := routineHistoryPage(r)
	if !ok {
		http.Error(w, "invalid history page", http.StatusBadRequest)
		return
	}
	runs, err := m.svc.HistoryPage(r.Context(), r.PathValue("id"), limit, before)
	if err != nil {
		writeHTTPError(w, "listing routine history", err)
		return
	}
	if runs == nil {
		runs = []state.RoutineRun{}
	}
	httpx.WriteJSON(w, runs)
}

const (
	defaultRoutineHistoryLimit = 50
	maxRoutineHistoryLimit     = 200
)

// routineHistoryPage reads ?limit=&beforeCreatedAt=&beforeId=. History is
// always bounded; older runs are reached by passing the last run's cursor.
func routineHistoryPage(r *http.Request) (int, state.RoutineRunCursor, bool) {
	q := r.URL.Query()
	limit := defaultRoutineHistoryLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxRoutineHistoryLimit {
			return 0, state.RoutineRunCursor{}, false
		}
		limit = n
	}
	before := state.RoutineRunCursor{ID: q.Get("beforeId")}
	if raw := q.Get("beforeCreatedAt"); raw != "" || before.ID != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before.ID == "" {
			return 0, state.RoutineRunCursor{}, false
		}
		before.CreatedAt = n
	}
	return limit, before, true
}

func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	items, err := m.svc.ListWithLatestRun(r.Context())
	if err != nil {
		writeHTTPError(w, "listing routines", err)
		return
	}
	if items == nil {
		items = []state.Routine{}
	}
	httpx.WriteJSON(w, items)
}

func (m *Module) handleCreate(w http.ResponseWriter, r *http.Request) {
	input, ok := readRoutineInput(w, r)
	if !ok {
		return
	}
	item, err := m.svc.Create(r.Context(), input)
	if err != nil {
		writeHTTPError(w, "creating routine", err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, item)
}

func (m *Module) handleGet(w http.ResponseWriter, r *http.Request) {
	item, err := m.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeHTTPError(w, "getting routine", err)
		return
	}
	httpx.WriteJSON(w, item)
}

func (m *Module) handleUpdate(w http.ResponseWriter, r *http.Request) {
	input, ok := readRoutineInput(w, r)
	if !ok {
		return
	}
	item, err := m.svc.Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeHTTPError(w, "updating routine", err)
		return
	}
	httpx.WriteJSON(w, item)
}

func (m *Module) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := m.svc.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeHTTPError(w, "deleting routine", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readRoutineInput(w http.ResponseWriter, r *http.Request) (Input, bool) {
	var req routineRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return Input{}, false
	}
	input, err := req.input()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return Input{}, false
	}
	return input, true
}

// writeHTTPError maps a routine service error to an HTTP response.
func writeHTTPError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, state.ErrRoutineNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNameConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, state.ErrRoutineRunActive):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		httpx.ServerError(w, operation, err)
	}
}

// Stats summarises every stored run of one routine.
type Stats struct {
	TotalRuns       int            `json:"totalRuns"`
	States          map[string]int `json:"states"`
	AverageDuration *float64       `json:"averageDurationMs"`
	TotalCost       float64        `json:"totalCost"`
	TotalEstCost    float64        `json:"totalEstCost"`
	CostSessions    int            `json:"costSessions"`
	MissingSessions int            `json:"missingSessions"`
}

func (m *Module) handleStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := m.svc.Get(r.Context(), id); err != nil {
		writeHTTPError(w, "getting routine", err)
		return
	}
	runs, err := m.svc.store.ListRoutineRuns(r.Context(), id)
	if err != nil {
		writeHTTPError(w, "reading routine stats", err)
		return
	}
	httpx.WriteJSON(w, summarizeRuns(r.Context(), runs, m.svc.sessionUsage))
}

func (s *Service) sessionUsage(ctx context.Context, key state.Key) (map[string]platforms.Usage, error) {
	if s.platforms == nil {
		return nil, platforms.ErrNotFound
	}
	adapter, ok := s.platforms.Get(platforms.ID(key.Platform))
	if !ok {
		return nil, platforms.ErrNotFound
	}
	reader, ok := adapter.(platforms.UsageReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	return reader.SessionUsage(ctx, key.SessionID)
}

// ponytail: scan stored runs on demand; persist billing snapshots if per-run costs are needed.
func summarizeRuns(ctx context.Context, runs []state.RoutineRun, usage func(context.Context, state.Key) (map[string]platforms.Usage, error)) Stats {
	stats := Stats{TotalRuns: len(runs), States: make(map[string]int)}
	var duration int64
	var completed int
	sessions := make(map[state.Key]bool)
	counted := make(map[state.Key]bool)
	for _, run := range runs {
		stats.States[run.State]++
		if run.State != "running" && run.StartedAt > 0 && run.FinishedAt >= run.StartedAt {
			duration += run.FinishedAt - run.StartedAt
			completed++
		}
		key := state.Key{Platform: run.Platform, SessionID: run.SessionID}
		if key.SessionID == "" || sessions[key] {
			continue
		}
		sessions[key] = true
		items, err := usage(ctx, key)
		if err != nil {
			stats.MissingSessions++
			continue
		}
		stats.CostSessions++
		for id, item := range items {
			itemKey := state.Key{Platform: key.Platform, SessionID: id}
			if !counted[itemKey] {
				counted[itemKey] = true
				stats.TotalCost += item.Cost
				stats.TotalEstCost += item.EstCost
			}
		}
	}
	if completed > 0 {
		average := float64(duration) / float64(completed)
		stats.AverageDuration = &average
	}
	return stats
}
