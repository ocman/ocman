package ocv2

// V1Session maps a v2 Session.Info to the v1 session object.
func V1Session(s map[string]any) map[string]any {
	t := obj(s, "time")
	out := map[string]any{
		"id": str(s, "id"), "projectID": str(s, "projectID"),
		"directory": str(obj(s, "location"), "directory"),
		"title":     str(s, "title"), "version": "2",
		"time":       map[string]any{"created": t["created"], "updated": t["updated"]},
		"permission": V1Rules(arr(s, "permissions")),
	}
	if p := str(s, "parentID"); p != "" {
		out["parentID"] = p
	}
	if a := t["archived"]; a != nil {
		out["time"].(map[string]any)["archived"] = a
	}
	if r := obj(s, "revert"); r != nil {
		out["revert"] = r
	}
	return out
}

// v1BuiltinAgents are OpenCode's own agents; v2 dropped the `native`
// flag ocman uses to label them.
var v1BuiltinAgents = map[string]bool{
	"build": true, "plan": true, "general": true, "explore": true,
	"title": true, "summary": true, "compaction": true,
}

// V1Agents maps v2 Agent.Info entries to the v1 /agent list.
func V1Agents(agents []any) []map[string]any {
	out := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		m, _ := a.(map[string]any)
		entry := map[string]any{
			"name": str(m, "id"), "description": str(m, "description"),
			"mode": str(m, "mode"), "hidden": m["hidden"] == true,
			"native": v1BuiltinAgents[str(m, "id")], "color": str(m, "color"),
		}
		if model := obj(m, "model"); model != nil {
			entry["model"] = str(model, "providerID") + "/" + str(model, "id")
		}
		out = append(out, entry)
	}
	return out
}

// V1Commands maps v2 Command.Info entries to the v1 /command list.
func V1Commands(cmds []any) []map[string]any {
	out := make([]map[string]any, 0, len(cmds))
	for _, c := range cmds {
		m, _ := c.(map[string]any)
		out = append(out, map[string]any{"name": str(m, "name"), "description": str(m, "description"), "template": "", "source": "command"})
	}
	return out
}

// V1Providers folds v2 /api/provider, /api/model and /api/model/default
// into the v1 /provider response. Only enabled models are listed, and a
// provider counts as connected when it has one.
func V1Providers(providers, models []any, def map[string]any) map[string]any {
	type entry struct {
		v1     map[string]any
		models map[string]any
	}
	byID := map[string]*entry{}
	var order []string
	for _, p := range providers {
		m, _ := p.(map[string]any)
		id := str(m, "id")
		byID[id] = &entry{v1: map[string]any{"id": id, "name": str(m, "name")}, models: map[string]any{}}
		order = append(order, id)
	}
	for _, mo := range models {
		m, _ := mo.(map[string]any)
		if m["enabled"] == false {
			continue
		}
		pid := str(m, "providerID")
		e := byID[pid]
		if e == nil {
			e = &entry{v1: map[string]any{"id": pid, "name": pid}, models: map[string]any{}}
			byID[pid] = e
			order = append(order, pid)
		}
		variants := map[string]any{}
		for _, v := range arr(m, "variants") {
			vm, _ := v.(map[string]any)
			variants[str(vm, "id")] = map[string]any{}
		}
		limit := obj(m, "limit")
		// Model.Ref (session model, POST …/model) addresses a model by
		// Model.Info.id; modelID is the provider's own name for it.
		modelID := str(m, "id")
		e.models[modelID] = map[string]any{
			"id": modelID, "name": str(m, "name"), "status": str(m, "status"),
			"variants": variants, "limit": map[string]any{"context": limit["context"], "output": limit["output"]},
		}
	}
	all := make([]any, 0, len(order))
	connected := []any{}
	for _, id := range order {
		e := byID[id]
		e.v1["models"] = e.models
		all = append(all, e.v1)
		if len(e.models) > 0 {
			connected = append(connected, id)
		}
	}
	defaults := map[string]any{}
	if def != nil {
		defaults[str(def, "providerID")] = str(def, "id")
	}
	return map[string]any{"all": all, "connected": connected, "default": defaults}
}

// MergeConfig folds v2 /api/config documents (lowest priority first)
// into one v1-style config object and restores the v1 keys ocman reads:
// `small_model` (now agents.title.model) and `mcp.<name>` (now possibly
// mcp.servers.<name>).
func MergeConfig(entries []any) map[string]any {
	merged := map[string]any{}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if str(m, "type") != "document" {
			continue
		}
		deepMerge(merged, obj(m, "info"))
	}
	if _, ok := merged["small_model"]; !ok {
		switch title := obj(obj(merged, "agents"), "title")["model"].(type) {
		case string:
			merged["small_model"] = title
		case map[string]any:
			merged["small_model"] = str(title, "providerID") + "/" + str(title, "id")
		}
	}
	if mcp := obj(merged, "mcp"); mcp != nil {
		for name, srv := range obj(mcp, "servers") {
			if _, ok := mcp[name]; !ok {
				mcp[name] = srv
			}
		}
	}
	return merged
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				deepMerge(dm, sm)
				continue
			}
			cp := map[string]any{}
			deepMerge(cp, sm)
			dst[k] = cp
			continue
		}
		dst[k] = v
	}
}
