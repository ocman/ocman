package owned

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

// Only joined headless consumers update this cache. Browser replay must never
// restore an old task after its completion has already been observed.
type backgroundState struct {
	mu       sync.RWMutex
	sessions map[string]platforms.BackgroundSnapshot
}

func (a *Adapter) observeBackground(raw []byte, kind string) {
	var event struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Payload    json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if len(event.Payload) > 0 {
		a.observeBackground(event.Payload, "")
		return
	}
	if event.Type != "" {
		kind = event.Type
	}
	props := event.Properties
	if len(props) == 0 {
		props = raw
	}
	a.background.mu.Lock()
	defer a.background.mu.Unlock()
	if a.background.sessions == nil {
		a.background.sessions = map[string]platforms.BackgroundSnapshot{}
	}
	switch kind {
	case "server.connected":
		var snapshot struct {
			Counts  map[string]int             `json:"backgroundTasks"`
			Details map[string]json.RawMessage `json:"backgroundTaskDetails"`
		}
		if json.Unmarshal(props, &snapshot) != nil || snapshot.Counts == nil {
			return
		}
		clear(a.background.sessions)
		for id, count := range snapshot.Counts {
			if count >= 0 {
				a.background.sessions[id] = platforms.BackgroundSnapshot{Count: count, Tasks: decodeBackgroundTasks(snapshot.Details[id])}
			}
		}
	case "ocman.session.background":
		var update struct {
			SessionID string          `json:"sessionID"`
			Count     *int            `json:"count"`
			Tasks     json.RawMessage `json:"tasks"`
		}
		if json.Unmarshal(props, &update) == nil && update.SessionID != "" && update.Count != nil && *update.Count >= 0 {
			a.background.sessions[update.SessionID] = platforms.BackgroundSnapshot{Count: *update.Count, Tasks: decodeBackgroundTasks(update.Tasks)}
		}
	}
}

// A malformed detail must not erase an otherwise valid absolute count. Work
// without usable details is still a finite task and continues to gate archives.
func decodeBackgroundTasks(raw json.RawMessage) []db.BackgroundTask {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var tasks []db.BackgroundTask
	for _, item := range items {
		var task db.BackgroundTask
		if json.Unmarshal(item, &task) == nil && task.ID != "" {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func (a *Adapter) backgroundSnapshots(ctx context.Context) (map[string]platforms.BackgroundSnapshot, error) {
	overrides, err := a.store.BackgroundLifecycles(ctx, a.owner, a.source.Platform, a.source.SourceKey)
	if err != nil {
		return nil, err
	}
	a.background.mu.RLock()
	defer a.background.mu.RUnlock()
	out := map[string]platforms.BackgroundSnapshot{}
	for id, snapshot := range a.background.sessions {
		snapshot.Tasks = slices.Clone(snapshot.Tasks)
		seen := map[string]bool{}
		tasks := snapshot.Tasks[:0]
		for _, task := range snapshot.Tasks {
			if snapshot.Count == 0 || task.ID == "" || seen[task.ID] {
				continue
			}
			seen[task.ID] = true
			if lifecycle := overrides[id][task.ID]; lifecycle != "" {
				task.Lifecycle = lifecycle
			}
			if task.Lifecycle != "service" {
				task.Lifecycle = "task"
			}
			tasks = append(tasks, task)
		}
		snapshot.Tasks = tasks
		out[id] = snapshot
	}
	return out, nil
}

func serviceCount(snapshot platforms.BackgroundSnapshot) int {
	n := 0
	for _, task := range snapshot.Tasks {
		if task.Lifecycle == "service" {
			n++
		}
	}
	return min(n, snapshot.Count)
}

func (a *Adapter) backgroundObservations(ctx context.Context, observations map[string]platforms.LiveState) error {
	snapshots, err := a.backgroundSnapshots(ctx)
	if err != nil {
		return err
	}
	for id, snapshot := range snapshots {
		live := observations[id]
		live.BackgroundTaskCount, live.BackgroundServiceCount = snapshot.Count, serviceCount(snapshot)
		observations[id] = live
	}
	return nil
}

func (a *Adapter) BackgroundTasks(ctx context.Context, id string) (*platforms.BackgroundSnapshot, error) {
	native, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshots, err := a.backgroundSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := snapshots[native]
	return &snapshot, nil
}

func (a *Adapter) SetBackgroundLifecycle(ctx context.Context, req platforms.SetBackgroundLifecycleRequest) error {
	if req.Lifecycle != "task" && req.Lifecycle != "service" {
		return sessioncatalog.ErrInvalid
	}
	snapshot, err := a.BackgroundTasks(ctx, req.SessionID)
	if err != nil {
		return err
	}
	for _, task := range snapshot.Tasks {
		if task.ID == req.TaskID {
			return a.store.SetBackgroundLifecycle(ctx, req.SessionID, req.TaskID, req.Lifecycle)
		}
	}
	return platforms.ErrNotFound
}
