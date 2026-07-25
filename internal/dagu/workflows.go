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
	id, err := newRunID()
	if err != nil {
		return Run{}, err
	}
	compiled, err := Compile(definition, CompileOptions{RunID: id})
	if err != nil {
		return Run{}, err
	}
	if len(compiled.Children) > 0 {
		return Run{}, fmt.Errorf("workflow maps over a subworkflow; start it through the manager so child DAGs reach the DAGs directory")
	}
	return c.StartSpec(ctx, definition.ID, id, compiled.Spec)
}

// StartSpec posts an already-compiled spec under a caller-chosen run ID,
// which lets ocman use its own run ID as the dagu dagRunId.
func (c *Client) StartSpec(ctx context.Context, name, id string, spec []byte) (Run, error) {
	body := struct {
		Spec     string `json:"spec"`
		Name     string `json:"name"`
		DAGRunID string `json:"dagRunId"`
	}{string(spec), name, id}
	var response struct {
		DAGRunID string `json:"dagRunId"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/dag-runs", body, &response); err != nil {
		return Run{}, err
	}
	if response.DAGRunID != id {
		return Run{}, fmt.Errorf("dagu returned run ID %q, want %q", response.DAGRunID, id)
	}
	return Run{ID: id, Name: name}, nil
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
		err := c.doJSON(ctx, http.MethodGet, path+"/steps/"+url.PathEscape(node.Name)+"/log?tail=1000&stream=stdout", nil, &log)
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
