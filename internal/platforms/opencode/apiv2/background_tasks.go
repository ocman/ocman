package ocv2

import (
	"net/http"
	"net/url"
	"sort"
)

func init() {
	handle(http.MethodPost, `/session/`+sid+`/background/`+sid+`/stop`, stopBackgroundTask)
}

// Verify ownership against the live shell list before terminating anything.
func stopBackgroundTask(c *compat, r *http.Request, m []string) (*http.Response, error) {
	q := location(requestDirectory(r))
	var shells data[[]map[string]any]
	if err := c.call(r.Context(), http.MethodGet, "/api/shell", q, nil, &shells); err != nil {
		return finish(r, 0, nil, err)
	}
	for _, shell := range shells.Data {
		if str(shell, "id") == m[2] && str(obj(shell, "metadata"), "sessionID") == m[1] && str(shell, "status") == "running" {
			err := c.call(r.Context(), http.MethodDelete, "/api/shell/"+url.PathEscape(m[2]), q, nil, nil)
			return finish(r, http.StatusNoContent, nil, err)
		}
	}
	return reply(r, http.StatusNotFound, map[string]any{"message": "background task not found in this session"}), nil
}

func (a *activity) backgroundDetails() map[string][]map[string]string {
	out := a.agentDetails()
	for id, owner := range a.shells {
		out[owner] = append(out[owner], map[string]string{"id": id, "description": a.commands[id]})
	}
	for _, tasks := range out {
		sort.Slice(tasks, func(i, j int) bool { return tasks[i]["id"] < tasks[j]["id"] })
	}
	return out
}
