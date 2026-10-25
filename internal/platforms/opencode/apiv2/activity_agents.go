package ocv2

import (
	"context"
	"errors"
	"net/http"
)

// Native parent links describe attached agent work, not ocman's independent
// worktree sessions. Keep them in the stream's disposable activity snapshot.
func (a *activity) rememberSession(info map[string]any) {
	id := str(info, "id")
	if id == "" {
		return
	}
	if a.parents == nil {
		a.parents = map[string]string{}
	}
	if a.titles == nil {
		a.titles = map[string]string{}
	}
	a.parents[id], a.titles[id] = str(info, "parentID"), str(info, "title")
}

func (a *activity) loadParents(ctx context.Context, c *compat, id string) error {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		if _, known := a.parents[id]; !known {
			info, err := c.session(ctx, id)
			if err != nil {
				var upstream *upstreamError
				if errors.As(err, &upstream) && upstream.resp.StatusCode == http.StatusNotFound {
					return nil // Deleted between the active snapshot and ownership read.
				}
				return err
			}
			a.rememberSession(info)
		}
		id = a.parents[id]
	}
	return nil
}

func (a *activity) ancestors(id string) []string {
	seen := map[string]bool{id: true}
	var out []string
	for parent := a.parents[id]; parent != "" && !seen[parent]; parent = a.parents[parent] {
		seen[parent] = true
		out = append(out, parent)
	}
	return out
}

func (a *activity) activeSessions() map[string]bool {
	out := map[string]bool{}
	for id := range a.foreground {
		out[id] = true
	}
	for _, id := range a.shells {
		out[id] = true
	}
	return out
}

func (a *activity) agentDetails() map[string][]map[string]string {
	out := map[string][]map[string]string{}
	for id := range a.activeSessions() {
		for _, parent := range a.ancestors(id) {
			out[parent] = append(out[parent], map[string]string{"id": id, "description": a.titles[id], "kind": "agent"})
		}
	}
	return out
}

func (t *translator) emitActivityFamily(id, dir string) error {
	if err := t.emitActivity(id, dir); err != nil {
		return err
	}
	for _, parent := range t.activity.ancestors(id) {
		if err := t.emitBackgroundCount(parent, dir); err != nil {
			return err
		}
	}
	return nil
}

func (t *translator) emitBackgroundFamily(id, dir string) error {
	if err := t.emitBackgroundCount(id, dir); err != nil {
		return err
	}
	for _, parent := range t.activity.ancestors(id) {
		if err := t.emitBackgroundCount(parent, dir); err != nil {
			return err
		}
	}
	return nil
}
