package prworkflow

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const sessionIdentityField = "session_id and platform (or directory for automatic session lookup)"

// sessionChecks reports whether checks targets a linked PR (host, repo and
// number, like link and watch) rather than a project commit (remote and sha).
func sessionChecks(args map[string]any) bool {
	for _, key := range []string{"host", "repo", "session_id"} {
		if _, ok := args[key]; ok {
			return true
		}
	}
	_, remote := args["remote"]
	_, sha := args["sha"]
	return !remote && !sha
}

func prToolRequest(action string, req mcplib.CallToolRequest) (method, path string, body []byte, err error) {
	args := req.GetArguments()
	present := func(key string) bool {
		value, ok := args[key].(string)
		return ok && strings.TrimSpace(value) != ""
	}
	q := url.Values{}
	payload := map[string]any{}
	var required []string
	var missing []string
	var numberRequired bool
	method = http.MethodGet
	switch {
	case action == "checks" && sessionChecks(args):
		numberRequired = true
		required = []string{"host", "repo"}
		if !present("session_id") || !present("platform") {
			missing = append(missing, sessionIdentityField)
		} else if path, err = sessionPath(req, "pull-request-checks"); err != nil {
			return
		}
	case action == "upstreams" || action == "list" || action == "checks" || action == "mergeability" || action == "launch":
		required = []string{"directory", "remote_id"}
		path = "/api/project/" + map[string]string{"upstreams": "upstreams", "list": "prs", "checks": "pr-checks", "mergeability": "pr-mergeability", "launch": "handle"}[action]
		if action != "upstreams" {
			required = append(required, "remote")
		}
		if action == "checks" {
			required = append(required, "sha")
		}
		numberRequired = action == "mergeability" || action == "launch"
		if action == "launch" {
			method = http.MethodPost
			required = append(required, "platform", "mode", "task")
			payload["type"] = "pr"
			// Cross-fork fetch approval stays in the dashboard. MCP never supplies
			// fetchHead, and therefore cannot bypass the requires_fetch response.
		}
	case sessionPRAction(action, args):
		numberRequired = action != "linked"
		if !present("session_id") || !present("platform") {
			missing = append(missing, sessionIdentityField)
		} else if path, err = sessionPath(req, "pull-requests"); err != nil {
			return
		}
		if action != "linked" {
			method = http.MethodPost
			payload["action"] = action
			if action == "link" {
				_, hasHost := args["host"]
				_, hasRepo := args["repo"]
				if hasHost || hasRepo {
					required = append(required, "host", "repo")
				} else {
					required = append(required, "remote")
				}
			} else {
				required = append(required, "host", "repo")
			}
		}
		if action == "feedback" || action == "checks_prompt" || action == "conflicts_prompt" {
			path = strings.Replace(path, "/pull-requests", "/pull-request-feedback", 1)
			payload["action"] = strings.TrimSuffix(action, "_prompt")
		}
	default:
		err = fmt.Errorf("unknown action; use help")
		return
	}
	for _, key := range required {
		if !present(key) {
			missing = append(missing, key)
		}
	}
	if _, ok := args["number"]; numberRequired && !ok {
		missing = append(missing, "number")
	}
	if len(missing) > 0 {
		err = missingFieldsError(action, missing)
		return
	}
	for key, field := range map[string]string{"directory": "dir", "remote_id": "remoteId", "remote": "remote", "platform": "platform", "host": "host", "repo": "repo", "sha": "sha", "state": "state", "mine": "mine", "mode": "mode", "task": "action"} {
		if _, ok := args[key]; !ok {
			continue
		}
		value, e := req.RequireString(key)
		if e != nil {
			err = fmt.Errorf("%s must be a string", key)
			return
		}
		q.Set(field, value)
		// task is meaningful only for launches; never override a session action.
		if key != "task" || action == "launch" {
			payload[field] = value
		}
	}
	if action == "launch" && (q.Get("action") != "handle" && q.Get("action") != "review") {
		err = fmt.Errorf("task must be handle or review")
		return
	}
	for _, key := range []string{"number", "page"} {
		if _, present := args[key]; !present {
			continue
		}
		n, e := req.RequireFloat(key)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 || n != math.Trunc(n) || n > float64(1<<31-1) {
			err = fmt.Errorf("%s must be a positive integer", key)
			return
		}
		payload[key] = int(n)
		q.Set(key, strconv.Itoa(int(n)))
	}
	if _, present := args["refresh"]; present {
		refresh, e := req.RequireBool("refresh")
		if e != nil {
			err = fmt.Errorf("refresh must be a boolean")
			return
		}
		if refresh {
			q.Set("refresh", "1")
		}
	}
	if _, present := args["detail"]; present {
		if _, ok := args["detail"].(bool); !ok {
			err = fmt.Errorf("detail must be a boolean")
			return
		}
	}
	path += "?" + q.Encode()
	body, err = json.Marshal(payload)
	return
}

func sessionPath(req mcplib.CallToolRequest, route string) (string, error) {
	id := req.GetString("session_id", "")
	if id == "" || strings.ContainsAny(id, "/\\?#%") || strings.Contains(id, "..") || strings.TrimSpace(id) != id {
		return "", fmt.Errorf("session_id must be a nonempty path segment")
	}
	return "/api/session/" + id + "/" + route, nil
}

// missingFieldsError names every absent field at once so one retry suffices.
func missingFieldsError(action string, missing []string) error {
	message := action + " is missing required fields: " + strings.Join(missing, "; ")
	if action == "checks" {
		if missing[0] == sessionIdentityField || missing[0] == "host" || missing[0] == "repo" {
			message += ". For a linked PR pass host, repo and number like link and watch; the head commit is resolved automatically. For a project commit pass directory, remote_id, remote and sha instead"
		} else {
			message += ". Alternatively pass host, repo and number for a PR linked to the session; its head commit is resolved automatically"
		}
	}
	if missing[0] == sessionIdentityField {
		message += ". " + prSessionIdentityGuidance
	}
	return fmt.Errorf("%s", message)
}
