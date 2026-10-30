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

func prToolRequest(action string, req mcplib.CallToolRequest) (method, path string, body []byte, err error) {
	q := url.Values{}
	payload := map[string]any{}
	var required []string
	method = http.MethodGet
	switch action {
	case "upstreams", "list", "checks", "mergeability", "launch":
		required = []string{"directory", "remote_id"}
		path = "/api/project/" + map[string]string{"upstreams": "upstreams", "list": "prs", "checks": "pr-checks", "mergeability": "pr-mergeability", "launch": "handle"}[action]
		if action != "upstreams" {
			required = append(required, "remote")
		}
		if action == "checks" {
			required = append(required, "sha")
		}
		if action == "launch" {
			method = http.MethodPost
			required = append(required, "platform", "mode", "task")
			payload["type"] = "pr"
			// Cross-fork fetch approval stays in the dashboard. MCP never supplies
			// fetchHead, and therefore cannot bypass the requires_fetch response.
		}
	case "linked", "link", "unlink", "watch", "unwatch", "feedback", "checks_prompt", "conflicts_prompt":
		required = []string{"session_id", "platform"}
		id, e := req.RequireString("session_id")
		if e != nil || id == "" || strings.ContainsAny(id, "/\\?#%") || strings.Contains(id, "..") || strings.TrimSpace(id) != id {
			err = fmt.Errorf("session_id must be a nonempty path segment")
			return
		}
		path = "/api/session/" + id + "/pull-requests"
		if action != "linked" {
			method = http.MethodPost
			payload["action"] = action
			if action == "link" {
				args := req.GetArguments()
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
			path = "/api/session/" + id + "/pull-request-feedback"
			payload["action"] = strings.TrimSuffix(action, "_prompt")
		}
	default:
		err = fmt.Errorf("unknown action; use help")
		return
	}
	for _, key := range required {
		if value, e := req.RequireString(key); e != nil || strings.TrimSpace(value) == "" {
			err = fmt.Errorf("%s is required", key)
			return
		}
	}
	for key, field := range map[string]string{"directory": "dir", "remote_id": "remoteId", "remote": "remote", "platform": "platform", "host": "host", "repo": "repo", "sha": "sha", "state": "state", "mine": "mine", "mode": "mode", "task": "action"} {
		if _, ok := req.GetArguments()[key]; !ok {
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
		_, present := req.GetArguments()[key]
		needsNumber := key == "number" && action != "upstreams" && action != "list" && action != "checks" && action != "linked"
		if !present && !needsNumber {
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
	if _, present := req.GetArguments()["refresh"]; present {
		refresh, e := req.RequireBool("refresh")
		if e != nil {
			err = fmt.Errorf("refresh must be a boolean")
			return
		}
		if refresh {
			q.Set("refresh", "1")
		}
	}
	path += "?" + q.Encode()
	body, err = json.Marshal(payload)
	return
}
