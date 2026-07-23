package dagu

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/NoUseFreak/ocman/internal/workflows"
	"gopkg.in/yaml.v3"
)

type Run struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
	Nodes  []Node `json:"nodes,omitempty"`
}

type Node struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Depends []string `json:"depends,omitempty"`
	Error   string   `json:"error,omitempty"`
	Log     string   `json:"log,omitempty"`
}

type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient(endpoint string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: client}
}

func (c *Client) Start(ctx context.Context, definition workflows.Definition) (Run, error) {
	spec, err := commandSpec(definition)
	if err != nil {
		return Run{}, err
	}
	id, err := newRunID()
	if err != nil {
		return Run{}, err
	}
	body := struct {
		Spec     string `json:"spec"`
		Name     string `json:"name"`
		DAGRunID string `json:"dagRunId"`
	}{string(spec), definition.ID, id}
	var response struct {
		DAGRunID string `json:"dagRunId"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/dag-runs", body, &response); err != nil {
		return Run{}, err
	}
	if response.DAGRunID != id {
		return Run{}, fmt.Errorf("dagu returned run ID %q, want %q", response.DAGRunID, id)
	}
	return Run{ID: id, Name: definition.ID}, nil
}

func (c *Client) GetRun(ctx context.Context, name, id string) (Run, error) {
	path := runPath(name, id)
	var response struct {
		DAGRunDetails struct {
			DAGRunID string `json:"dagRunId"`
			Name     string `json:"name"`
			Status   string `json:"statusLabel"`
			Nodes    []struct {
				Step struct {
					Name    string   `json:"name"`
					Depends []string `json:"depends"`
				} `json:"step"`
				Status string `json:"statusLabel"`
				Error  string `json:"error"`
			} `json:"nodes"`
		} `json:"dagRunDetails"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return Run{}, err
	}
	detail := response.DAGRunDetails
	run := Run{ID: detail.DAGRunID, Name: detail.Name, Status: detail.Status}
	for _, source := range detail.Nodes {
		node := Node{Name: source.Step.Name, Status: source.Status, Depends: source.Step.Depends, Error: source.Error}
		var log struct {
			Content string `json:"content"`
		}
		err := c.doJSON(ctx, http.MethodGet, path+"/steps/"+url.PathEscape(node.Name)+"/log?tail=1000", nil, &log)
		if err != nil && !isHTTPStatus(err, http.StatusNotFound) {
			return Run{}, err
		}
		node.Log = log.Content
		run.Nodes = append(run.Nodes, node)
	}
	return run, nil
}

func (c *Client) Cancel(ctx context.Context, name, id string) error {
	return c.doJSON(ctx, http.MethodPost, runPath(name, id)+"/stop", nil, nil)
}

func runPath(name, id string) string {
	return "/api/v1/dag-runs/" + url.PathEscape(name) + "/" + url.PathEscape(id)
}

type httpError struct {
	status int
	body   string
}

func (e httpError) Error() string { return fmt.Sprintf("Dagu API returned %d: %s", e.status, e.body) }
func isHTTPStatus(err error, status int) bool {
	var apiErr httpError
	return errors.As(err, &apiErr) && apiErr.status == status
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return httpError{status: resp.StatusCode, body: strings.TrimSpace(string(message))}
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

func newRunID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate Dagu run ID: %w", err)
	}
	return "ocman-" + hex.EncodeToString(random), nil
}

func commandSpec(definition workflows.Definition) ([]byte, error) {
	if definition.ID == "" || len(definition.Nodes) == 0 {
		return nil, fmt.Errorf("command workflow requires an ID and nodes")
	}
	for _, trigger := range definition.Triggers {
		if trigger.Type != workflows.TriggerManual {
			return nil, fmt.Errorf("dagu workflows support manual triggers only")
		}
	}
	if len(definition.Secrets) > 0 || len(definition.Pools) > 0 || definition.Workspace != nil || definition.Limits != nil || definition.FailFast || len(definition.SubworkflowRefs) > 0 {
		return nil, fmt.Errorf("dagu command workflows do not support native secrets, pools, workspaces, limits, or fail-fast behavior")
	}
	names := make(map[string]string, len(definition.Nodes))
	for _, node := range definition.Nodes {
		if node.ID == "" || node.Name == "" || node.Type != "command" || len(node.Command) == 0 {
			return nil, fmt.Errorf("dagu workflows support command nodes only")
		}
		if len(node.Permission) > 0 || len(node.Resources) > 0 || node.Lease != nil || node.Repeat != nil {
			return nil, fmt.Errorf("dagu command nodes do not support native permissions, resources, leases, or repeats")
		}
		for _, arg := range node.Command {
			if strings.Contains(arg, "${nodes.") {
				return nil, fmt.Errorf("dagu command workflows do not support native node interpolation")
			}
		}
		for _, value := range node.Environment {
			if strings.Contains(value, "${nodes.") {
				return nil, fmt.Errorf("dagu command workflows do not support native node interpolation")
			}
		}
		names[node.ID] = node.Name
	}
	for _, dependency := range definition.Dependencies {
		if names[dependency.From] == "" || names[dependency.To] == "" || dependency.Condition != "" {
			return nil, fmt.Errorf("dagu workflows require unconditional dependencies between command nodes")
		}
	}
	type step struct {
		Name    string            `yaml:"name"`
		Run     string            `yaml:"run"`
		Dir     string            `yaml:"dir,omitempty"`
		Env     map[string]string `yaml:"env,omitempty"`
		Depends []string          `yaml:"depends,omitempty"`
	}
	spec := struct {
		WorkingDir string `yaml:"working_dir,omitempty"`
		Steps      []step `yaml:"steps"`
	}{WorkingDir: definition.Directory}
	for _, node := range definition.Nodes {
		item := step{Name: node.Name, Run: shellCommand(node.Command), Env: node.Environment}
		for _, dependency := range definition.Dependencies {
			if dependency.To == node.ID {
				item.Depends = append(item.Depends, names[dependency.From])
			}
		}
		spec.Steps = append(spec.Steps, item)
	}
	return yaml.Marshal(spec)
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_@%+=:,./-", r)
		}) == -1 {
			quoted[i] = arg
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}
