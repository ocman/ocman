package ocv2

import "strings"

// v2 renamed several built-in tools (and the permission actions that
// gate them). ocman's renderers, commit capture, changes pane and
// permission inheritance key on the v1 names, so translate at the edge.
var v2ToV1Names = map[string]string{"shell": "bash", "patch": "apply_patch", "subagent": "task"}

// V1ToolName returns the v1 name for a v2 tool.
func V1ToolName(name string) string {
	if v1, ok := v2ToV1Names[name]; ok {
		return v1
	}
	return name
}

// V1Action maps a v2 permission action to the v1 permission key.
func V1Action(action string) string {
	switch action {
	case "shell":
		return "bash"
	case "subagent":
		return "task"
	}
	return action
}

// V2Action maps a v1 permission key to the v2 action. v2 folds write
// and patch into edit.
func V2Action(permission string) string {
	switch permission {
	case "bash":
		return "shell"
	case "task":
		return "subagent"
	case "write", "patch", "apply_patch":
		return "edit"
	}
	return permission
}

func convertTool(item map[string]any) map[string]any {
	name := V1ToolName(str(item, "name"))
	state := obj(item, "state")
	t := obj(item, "time")
	input, _ := state["input"].(map[string]any)
	if input == nil {
		input = map[string]any{} // streaming: raw JSON text so far
	} else {
		input = v1Input(name, input)
	}
	var texts []string
	for _, c := range arr(state, "content") {
		item, _ := c.(map[string]any)
		if s := str(item, "text"); s != "" {
			texts = append(texts, s)
		}
	}
	output := strings.Join(texts, "\n")
	status := str(state, "status")
	if status == "streaming" {
		status = "pending"
	}
	v1State := map[string]any{
		"status":   status,
		"input":    input,
		"title":    toolTitle(input),
		"metadata": v1Metadata(name, obj(state, "metadata"), output),
		"time":     span("start", t["created"], "end", t["completed"]),
	}
	if status == "completed" || status == "error" {
		v1State["output"] = output
	}
	if e := obj(state, "error"); e != nil {
		v1State["error"] = str(e, "message")
	}
	return map[string]any{"type": "tool", "tool": name, "callID": str(item, "id"), "state": v1State}
}

// v1Input adds the v1 `filePath` spelling for the file tools that now
// take `path` (glob/grep keep `path`: it is a search root there).
func v1Input(tool string, in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	if p, ok := in["path"].(string); ok && (tool == "read" || tool == "edit" || tool == "write") {
		if _, has := in["filePath"]; !has {
			out["filePath"] = p
		}
	}
	return out
}

func toolTitle(input map[string]any) string {
	for _, k := range []string{"description", "filePath", "command", "pattern", "url"} {
		if s, ok := input[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// v1Metadata rewrites v2 tool metadata into the keys ocman's v1
// consumers read: bash output, edit `filediff`, apply_patch `files`, and
// the task child session.
func v1Metadata(tool string, md map[string]any, output string) map[string]any {
	out := map[string]any{}
	for k, v := range md {
		out[k] = v
	}
	files := fileDiffs(md)
	switch tool {
	case "bash":
		out["output"] = output
	case "edit", "write":
		if len(files) > 0 {
			f := files[0]
			out["filediff"] = map[string]any{
				"file": str(f, "file"), "patch": str(f, "patch"),
				"additions": f["additions"], "deletions": f["deletions"],
			}
			out["diff"] = str(f, "patch")
			out["filepath"] = str(f, "file")
		}
	case "apply_patch":
		v1Files := make([]any, 0, len(files))
		for _, f := range files {
			v1Files = append(v1Files, map[string]any{
				"filePath": str(f, "file"), "relativePath": str(f, "file"),
				"type": patchType(str(f, "status")), "patch": str(f, "patch"),
				"additions": f["additions"], "deletions": f["deletions"],
			})
		}
		out["files"] = v1Files
	case "task":
		if sid := str(md, "sessionID"); sid != "" {
			out["sessionId"] = sid
		}
	}
	return out
}

func fileDiffs(md map[string]any) []map[string]any {
	var out []map[string]any
	for _, f := range arr(md, "files") {
		if m, ok := f.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func patchType(status string) string {
	switch status {
	case "added":
		return "add"
	case "deleted":
		return "delete"
	}
	return "update"
}

// V1Rules maps a v2 ruleset {action, resource, effect} to v1 rules
// {permission, pattern, action}.
func V1Rules(rules []any) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, r := range rules {
		m, _ := r.(map[string]any)
		out = append(out, map[string]any{
			"permission": V1Action(str(m, "action")), "pattern": str(m, "resource"), "action": str(m, "effect"),
		})
	}
	return out
}

// V2Rules is the inverse of V1Rules.
func V2Rules(rules []any) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, r := range rules {
		m, _ := r.(map[string]any)
		out = append(out, map[string]any{
			"action": V2Action(str(m, "permission")), "resource": str(m, "pattern"), "effect": str(m, "action"),
		})
	}
	return out
}

// V1Permission maps a v2 Permission.Request to the v1 permission prompt.
func V1Permission(req map[string]any) map[string]any {
	md := map[string]any{}
	for k, v := range obj(req, "metadata") {
		md[k] = v
	}
	if files := fileDiffs(md); len(files) > 0 && md["diff"] == nil {
		var diffs, paths []string
		for _, f := range files {
			diffs = append(diffs, str(f, "patch"))
			paths = append(paths, str(f, "file"))
		}
		md["diff"] = strings.Join(diffs, "\n")
		md["filepath"] = strings.Join(paths, ", ")
	}
	resources := arr(req, "resources")
	if V1Action(str(req, "action")) == "bash" && md["command"] == nil && len(resources) > 0 {
		cmds := make([]string, 0, len(resources))
		for _, r := range resources {
			s, _ := r.(string)
			cmds = append(cmds, s)
		}
		md["command"] = strings.Join(cmds, " && ")
	}
	out := map[string]any{
		"id": str(req, "id"), "sessionID": str(req, "sessionID"),
		"permission": V1Action(str(req, "action")),
		"patterns":   nonNil(resources), "always": nonNil(arr(req, "save")),
		"metadata": md,
	}
	if src := obj(req, "source"); src != nil {
		out["tool"] = map[string]any{"messageID": str(src, "messageID"), "callID": str(src, "id")}
	}
	return out
}

// IsQuestionForm reports whether a v2 form came from the question tool.
func IsQuestionForm(form map[string]any) bool {
	return str(obj(form, "metadata"), "kind") == "question"
}

// V1Question maps a question-tool form to the v1 question prompt.
func V1Question(form map[string]any) map[string]any {
	questions := []any{}
	for _, f := range arr(form, "fields") {
		field, _ := f.(map[string]any)
		options := []any{}
		for _, o := range arr(field, "options") {
			opt, _ := o.(map[string]any)
			options = append(options, map[string]any{"label": str(opt, "label"), "description": str(opt, "description")})
		}
		questions = append(questions, map[string]any{
			"header": str(field, "title"), "question": str(field, "description"),
			"options": options, "multiple": str(field, "type") == "multiselect", "custom": field["custom"] != false,
		})
	}
	out := map[string]any{"id": str(form, "id"), "sessionID": str(form, "sessionID"), "questions": questions}
	if tool := obj(obj(form, "metadata"), "tool"); tool != nil {
		out["tool"] = map[string]any{"messageID": str(tool, "messageID"), "callID": str(tool, "id")}
	}
	return out
}

// FormAnswer maps v1 question answers (one label list per question) to
// a v2 form answer keyed by field. Multiselect fields take the list,
// string fields its first entry; unanswered fields are omitted.
func FormAnswer(form map[string]any, answers [][]string) map[string]any {
	out := map[string]any{}
	for i, f := range arr(form, "fields") {
		field, _ := f.(map[string]any)
		if i >= len(answers) || len(answers[i]) == 0 {
			continue
		}
		if str(field, "type") == "multiselect" {
			out[str(field, "key")] = answers[i]
		} else {
			out[str(field, "key")] = answers[i][0]
		}
	}
	return out
}

func nonNil(a []any) []any {
	if a == nil {
		return []any{}
	}
	return a
}
