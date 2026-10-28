package claudeprovider

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func backgroundCountEvent(id string, count int, tasks ...[]map[string]string) providerprotocol.Event {
	data := map[string]any{"sessionID": id, "count": count}
	if len(tasks) > 0 {
		data["tasks"] = tasks[0]
	}
	props, _ := json.Marshal(data)
	return providerprotocol.Event{Type: "ocman.session.background", Properties: props}
}

func (l *live) publishBackgroundCount(p *proc, count int) {
	l.mu.Lock()
	tasks := backgroundDetails(p)
	l.mu.Unlock()
	// Stop or a replacement child may have overtaken the task update.
	_, err := l.sup.PublishIf(func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.procs[p.id] == p && len(p.tasks) == count
	}, func(providerprotocol.Event) {}, backgroundCountEvent(p.id, count, tasks))
	if err != nil {
		l.publishFailed(p.id, err)
	}
}

// Caller holds l.mu. Only the authoritative task ledger determines membership.
func backgroundDetails(p *proc) []map[string]string {
	tasks := []map[string]string{}
	if p != nil {
		for id := range p.tasks {
			tasks = append(tasks, map[string]string{"id": id, "description": p.taskDetails[id]})
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i]["id"] < tasks[j]["id"] })
	return tasks
}

func (l *live) abortRequest(r *http.Request, id string) error {
	if r.Body == nil || r.ContentLength == 0 {
		return l.abort(r.Context(), id)
	}
	var req platforms.AbortRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.TaskID == "" {
		return l.abort(r.Context(), id)
	}
	l.mu.Lock()
	p := l.procs[id]
	found := p != nil && p.tasks[req.TaskID]
	l.mu.Unlock()
	if !found {
		return platforms.ErrNotFound
	}
	_, err := l.control(r.Context(), p, "", map[string]any{"subtype": "stop_task", "task_id": req.TaskID})
	return err
}
