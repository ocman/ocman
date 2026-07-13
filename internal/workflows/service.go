package workflows

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	StateActive     = "active"
	StatePaused     = "paused"
	StateSuccessful = "successful"
	StateCanceled   = "canceled"
	StateFailed     = "failed"

	NodePending    = "pending"
	NodeReady      = "ready"
	NodeSuccessful = "successful"
	NodeCanceled   = "canceled"
	NodeFailed     = "failed"
	NodeSkipped    = "skipped"

	AttemptWaiting    = "waiting"
	AttemptSuccessful = "successful"
	AttemptCanceled   = "canceled"
	AttemptFailed     = "failed"
	AttemptErrored    = "errored"
	AttemptDenied     = "denied"
	AttemptRunning    = "running"
)

type Definition struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Concurrency  int          `json:"concurrency"`
	Directory    string       `json:"directory,omitempty"`
	Nodes        []Node       `json:"nodes"`
	Dependencies []Dependency `json:"dependencies"`
}

type Node struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Command     []string          `json:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Permission  []PermissionRule  `json:"permission,omitempty"`
	Outputs     []Collector       `json:"outputs,omitempty"`
}

type PermissionRule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

type Collector struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

type Dependency struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Version struct {
	ID         string     `json:"id"`
	WorkflowID string     `json:"workflowId"`
	Name       string     `json:"name"`
	Revision   int        `json:"revision"`
	CreatedAt  int64      `json:"createdAt"`
	Definition Definition `json:"definition"`
}

type Run struct {
	ID          string `json:"id"`
	WorkflowID  string `json:"workflowId"`
	VersionID   string `json:"versionId"`
	State       string `json:"state"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	CompletedAt int64  `json:"completedAt,omitempty"`
}

type RunDetail struct {
	Run
	Version Version   `json:"version"`
	Nodes   []NodeRun `json:"nodes"`
}

type NodeRun struct {
	NodeID      string    `json:"nodeId"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	State       string    `json:"state"`
	ReadyAt     int64     `json:"readyAt,omitempty"`
	CompletedAt int64     `json:"completedAt,omitempty"`
	Attempts    []Attempt `json:"attempts"`
}

type Attempt struct {
	ID              int64             `json:"id"`
	Seq             int               `json:"seq"`
	State           string            `json:"state"`
	StartedAt       int64             `json:"startedAt"`
	CompletedAt     int64             `json:"completedAt,omitempty"`
	ExitCode        *int              `json:"exitCode,omitempty"`
	Stdout          string            `json:"stdout,omitempty"`
	Stderr          string            `json:"stderr,omitempty"`
	Error           string            `json:"error,omitempty"`
	Outputs         map[string]string `json:"outputs,omitempty"`
	StdoutTruncated bool              `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool              `json:"stderrTruncated,omitempty"`
}

type Store interface {
	InsertWorkflowVersion(state.WorkflowVersion) (state.WorkflowVersion, error)
	GetWorkflowVersion(string) (*state.WorkflowVersion, error)
	ListWorkflowVersions() ([]state.WorkflowVersion, error)
	InsertWorkflowRun(state.WorkflowRun) error
	GetWorkflowRun(string) (*state.WorkflowRun, error)
	ListWorkflowRuns() ([]state.WorkflowRun, error)
	ApproveWorkflowNode(string, string, int64) error
	StartWorkflowCommand(string, string, int64) (bool, error)
	CompleteWorkflowCommand(string, string, state.WorkflowCommandResult, int64) error
	SetWorkflowRunState(string, string, string, int64) error
}

type Deps struct {
	Store           Store
	Now             func() time.Time
	Notify          func(runID string)
	CommandExecutor CommandExecutor
}

type Service struct {
	store    Store
	now      func() time.Time
	notify   func(string)
	executor CommandExecutor
	mu       sync.Mutex
	running  map[string]map[string]*activeCommand
	stopping map[string]bool
}

type activeCommand struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewService(deps Deps) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	executor := deps.CommandExecutor
	if executor == nil {
		executor = localCommandExecutor{}
	}
	return &Service{store: deps.Store, now: now, notify: deps.Notify, executor: executor, running: make(map[string]map[string]*activeCommand), stopping: make(map[string]bool)}
}

func (s *Service) PublishJSON(_ context.Context, source []byte) (Version, error) {
	definition, canonical, err := decodeDefinition(source)
	if err != nil {
		return Version{}, err
	}
	if err := validateDefinition(definition); err != nil {
		return Version{}, err
	}
	now := s.now().UnixMilli()
	row := state.WorkflowVersion{
		ID:              newID("wfv"),
		WorkflowID:      definition.ID,
		Name:            definition.Name,
		MetadataVersion: definition.Version,
		DefinitionJSON:  string(canonical),
		Concurrency:     definition.Concurrency,
		CreatedAt:       now,
	}
	for position, node := range definition.Nodes {
		row.Nodes = append(row.Nodes, state.WorkflowNode{ID: node.ID, Name: node.Name, Type: node.Type, Position: position})
	}
	for _, dep := range definition.Dependencies {
		row.Dependencies = append(row.Dependencies, state.WorkflowDependency{From: dep.From, To: dep.To})
	}
	row, err = s.store.InsertWorkflowVersion(row)
	if err != nil {
		return Version{}, err
	}
	return versionFromState(row, definition), nil
}

func (s *Service) GetVersion(_ context.Context, id string) (Version, error) {
	row, err := s.store.GetWorkflowVersion(id)
	if err != nil {
		return Version{}, err
	}
	return versionFromRow(*row)
}

func (s *Service) ListVersions(_ context.Context) ([]Version, error) {
	rows, err := s.store.ListWorkflowVersions()
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, row := range rows {
		version, err := versionFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, nil
}

func (s *Service) Start(_ context.Context, versionID string) (RunDetail, error) {
	version, err := s.store.GetWorkflowVersion(versionID)
	if err != nil {
		return RunDetail{}, err
	}
	dependencies := make(map[string]bool, len(version.Dependencies))
	for _, dep := range version.Dependencies {
		dependencies[dep.To] = true
	}
	now := s.now().UnixMilli()
	run := state.WorkflowRun{
		ID:         newID("wfr"),
		WorkflowID: version.WorkflowID,
		VersionID:  version.ID,
		State:      StateActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	for _, node := range version.Nodes {
		nodeState := NodePending
		readyAt := int64(0)
		if !dependencies[node.ID] {
			nodeState = NodeReady
			readyAt = now
		}
		run.Nodes = append(run.Nodes, state.WorkflowNodeRun{NodeID: node.ID, Type: node.Type, Position: node.Position, State: nodeState, ReadyAt: readyAt})
	}
	if err := s.store.InsertWorkflowRun(run); err != nil {
		return RunDetail{}, err
	}
	detail, err := s.GetRun(context.Background(), run.ID)
	if err == nil {
		s.dispatchReady(detail)
	}
	s.changed(run.ID)
	return detail, err
}

func (s *Service) ListRuns(_ context.Context) ([]Run, error) {
	rows, err := s.store.ListWorkflowRuns()
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(rows))
	for _, row := range rows {
		out = append(out, runFromState(row))
	}
	return out, nil
}

func (s *Service) GetRun(ctx context.Context, id string) (RunDetail, error) {
	run, err := s.store.GetWorkflowRun(id)
	if err != nil {
		return RunDetail{}, err
	}
	version, err := s.GetVersion(ctx, run.VersionID)
	if err != nil {
		return RunDetail{}, err
	}
	detail := RunDetail{Run: runFromState(*run), Version: version, Nodes: make([]NodeRun, 0, len(run.Nodes))}
	for _, row := range run.Nodes {
		node := NodeRun{NodeID: row.NodeID, Name: row.Name, Type: row.Type, State: row.State, ReadyAt: row.ReadyAt, CompletedAt: row.CompletedAt, Attempts: make([]Attempt, 0, len(row.Attempts))}
		for _, attempt := range row.Attempts {
			outputs := map[string]string{}
			if err := json.Unmarshal([]byte(attempt.OutputsJSON), &outputs); err != nil {
				return RunDetail{}, fmt.Errorf("decoding command outputs: %w", err)
			}
			node.Attempts = append(node.Attempts, Attempt{ID: attempt.ID, Seq: attempt.Seq, State: attempt.State, StartedAt: attempt.StartedAt, CompletedAt: attempt.CompletedAt, ExitCode: attempt.ExitCode, Stdout: attempt.Stdout, Stderr: attempt.Stderr, Error: attempt.Error, Outputs: outputs, StdoutTruncated: attempt.StdoutTruncated, StderrTruncated: attempt.StderrTruncated})
		}
		detail.Nodes = append(detail.Nodes, node)
	}
	return detail, nil
}

func (s *Service) Approve(ctx context.Context, runID, nodeID string) (RunDetail, error) {
	if err := s.store.ApproveWorkflowNode(runID, nodeID, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	run, err := s.GetRun(ctx, runID)
	if err == nil {
		s.dispatchReady(run)
	}
	s.changed(runID)
	return run, err
}

func (s *Service) Pause(ctx context.Context, runID string) (RunDetail, error) {
	if err := s.store.SetWorkflowRunState(runID, StateActive, StatePaused, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.changed(runID)
	return s.GetRun(ctx, runID)
}

func (s *Service) Cancel(ctx context.Context, runID string) (RunDetail, error) {
	run, err := s.store.GetWorkflowRun(runID)
	if err != nil {
		return RunDetail{}, err
	}
	if run.State != StateActive && run.State != StatePaused {
		return RunDetail{}, fmt.Errorf("workflow run cannot be canceled from %s", run.State)
	}
	s.mu.Lock()
	s.stopping[runID] = true
	active := make([]*activeCommand, 0, len(s.running[runID]))
	for _, command := range s.running[runID] {
		active = append(active, command)
		command.cancel()
	}
	s.mu.Unlock()
	for _, command := range active {
		<-command.done
	}
	if err := s.store.SetWorkflowRunState(runID, run.State, StateCanceled, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.mu.Lock()
	delete(s.stopping, runID)
	s.mu.Unlock()
	s.changed(runID)
	return s.GetRun(ctx, runID)
}

func (s *Service) dispatchReady(run RunDetail) {
	if run.State != StateActive {
		return
	}
	definitions := make(map[string]Node, len(run.Version.Definition.Nodes))
	for _, node := range run.Version.Definition.Nodes {
		definitions[node.ID] = node
	}
	for _, nodeRun := range run.Nodes {
		if nodeRun.Type != "command" || nodeRun.State != NodeReady || len(nodeRun.Attempts) == 0 || nodeRun.Attempts[0].State != AttemptWaiting {
			continue
		}
		s.mu.Lock()
		if s.stopping[run.ID] || len(s.running[run.ID]) >= run.Version.Definition.Concurrency {
			s.mu.Unlock()
			return
		}
		if s.running[run.ID] == nil {
			s.running[run.ID] = make(map[string]*activeCommand)
		}
		if s.running[run.ID][nodeRun.NodeID] != nil {
			s.mu.Unlock()
			continue
		}
		started, err := s.store.StartWorkflowCommand(run.ID, nodeRun.NodeID, s.now().UnixMilli())
		if err != nil || !started {
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		active := &activeCommand{cancel: cancel, done: make(chan struct{})}
		s.running[run.ID][nodeRun.NodeID] = active
		s.mu.Unlock()
		definition := definitions[nodeRun.NodeID]
		go s.executeCommand(ctx, active, run.ID, run.Version.Definition.Directory, definition)
	}
}

func (s *Service) executeCommand(ctx context.Context, active *activeCommand, runID, directory string, node Node) {
	result := s.executor.Execute(ctx, CommandRequest{Directory: directory, Command: node.Command, Environment: node.Environment, Permission: node.Permission, Outputs: node.Outputs})
	stopOwner := false
	if result.State != AttemptSuccessful && result.State != AttemptCanceled {
		stopOwner = s.stopSiblingCommands(runID, node.ID)
		if !stopOwner {
			result.State = AttemptCanceled
			result.Error = "canceled after sibling failure"
		}
	}
	outputs, err := json.Marshal(result.Outputs)
	if err != nil {
		result.State, result.Error = AttemptErrored, err.Error()
		outputs = []byte("{}")
	}
	_ = s.store.CompleteWorkflowCommand(runID, node.ID, state.WorkflowCommandResult{
		State: result.State, ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr, Error: result.Error,
		OutputsJSON: string(outputs), StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated,
	}, s.now().UnixMilli())
	s.mu.Lock()
	delete(s.running[runID], node.ID)
	if len(s.running[runID]) == 0 {
		delete(s.running, runID)
	}
	if stopOwner {
		delete(s.stopping, runID)
	}
	close(active.done)
	s.mu.Unlock()
	if run, err := s.GetRun(context.Background(), runID); err == nil {
		s.dispatchReady(run)
	}
	s.changed(runID)
}

func (s *Service) stopSiblingCommands(runID, nodeID string) bool {
	s.mu.Lock()
	if s.stopping[runID] {
		s.mu.Unlock()
		return false
	}
	s.stopping[runID] = true
	var siblings []*activeCommand
	for id, command := range s.running[runID] {
		if id != nodeID {
			siblings = append(siblings, command)
			command.cancel()
		}
	}
	s.mu.Unlock()
	for _, command := range siblings {
		<-command.done
	}
	return true
}

func (s *Service) changed(runID string) {
	if s.notify != nil {
		s.notify(runID)
	}
}

func decodeDefinition(source []byte) (Definition, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	var definition Definition
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, nil, fmt.Errorf("invalid workflow JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Definition{}, nil, fmt.Errorf("invalid workflow JSON: trailing content")
	}
	canonical, err := json.Marshal(definition)
	if err != nil {
		return Definition{}, nil, fmt.Errorf("encoding workflow definition: %w", err)
	}
	return definition, canonical, nil
}

func validateDefinition(definition Definition) error {
	if definition.ID == "" || definition.Name == "" || definition.Version == "" {
		return fmt.Errorf("id, name, and version are required")
	}
	if definition.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be positive")
	}
	if len(definition.Nodes) == 0 {
		return fmt.Errorf("at least one node is required")
	}
	nodes := make(map[string]bool, len(definition.Nodes))
	for _, node := range definition.Nodes {
		if node.ID == "" || node.Name == "" || node.Type == "" {
			return fmt.Errorf("node id, name, and type are required")
		}
		if node.Type != "approval" && node.Type != "command" {
			return fmt.Errorf("unsupported node type %q", node.Type)
		}
		if node.Type == "command" {
			if err := validateCommandNode(definition.Directory, node); err != nil {
				return fmt.Errorf("node %q: %w", node.ID, err)
			}
		}
		if nodes[node.ID] {
			return fmt.Errorf("duplicate node %q", node.ID)
		}
		nodes[node.ID] = true
	}
	indegree := make(map[string]int, len(nodes))
	edges := make(map[string][]string, len(nodes))
	seenDependencies := make(map[Dependency]bool, len(definition.Dependencies))
	for _, dep := range definition.Dependencies {
		if dep.From == "" || dep.To == "" {
			return fmt.Errorf("dependency endpoints are required")
		}
		if dep.From == dep.To {
			return fmt.Errorf("self dependency for node %q", dep.From)
		}
		if !nodes[dep.From] {
			return fmt.Errorf("dependency references missing node %q", dep.From)
		}
		if !nodes[dep.To] {
			return fmt.Errorf("dependency references missing node %q", dep.To)
		}
		if seenDependencies[dep] {
			return fmt.Errorf("duplicate dependency %q -> %q", dep.From, dep.To)
		}
		seenDependencies[dep] = true
		edges[dep.From] = append(edges[dep.From], dep.To)
		indegree[dep.To]++
	}
	queue := make([]string, 0, len(nodes))
	for id := range nodes {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range edges[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("workflow contains a cycle")
	}
	return nil
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateCommandNode(directory string, node Node) error {
	if directory == "" || !filepath.IsAbs(directory) {
		return fmt.Errorf("workflow directory must be absolute for command nodes")
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return fmt.Errorf("workflow directory must exist")
	}
	if len(node.Command) == 0 || node.Command[0] == "" {
		return fmt.Errorf("command is required")
	}
	for _, arg := range node.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command contains NUL")
		}
	}
	for key, value := range node.Environment {
		if !environmentName.MatchString(key) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid environment variable %q", key)
		}
	}
	for _, rule := range node.Permission {
		if rule.Permission != "bash" || rule.Pattern == "" {
			return fmt.Errorf("command permission requires bash and a pattern")
		}
		switch rule.Action {
		case "allow", "deny", "ask":
		default:
			return fmt.Errorf("invalid permission action %q", rule.Action)
		}
	}
	seen := make(map[string]bool, len(node.Outputs))
	if len(node.Outputs) > 32 {
		return fmt.Errorf("at most 32 collectors are allowed")
	}
	for _, output := range node.Outputs {
		if output.Name == "" || seen[output.Name] {
			return fmt.Errorf("collector names must be present and unique")
		}
		seen[output.Name] = true
		switch output.Type {
		case "text", "git_diff":
			if output.Path != "" {
				return fmt.Errorf("collector %q does not accept a path", output.Name)
			}
		case "file", "json_file":
			clean := filepath.Clean(output.Path)
			if output.Path == "" || filepath.IsAbs(output.Path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("collector %q path must stay inside workflow directory", output.Name)
			}
		default:
			return fmt.Errorf("unsupported collector type %q", output.Type)
		}
	}
	return nil
}

func versionFromRow(row state.WorkflowVersion) (Version, error) {
	var definition Definition
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &definition); err != nil {
		return Version{}, fmt.Errorf("decoding stored workflow definition: %w", err)
	}
	return versionFromState(row, definition), nil
}

func versionFromState(row state.WorkflowVersion, definition Definition) Version {
	return Version{ID: row.ID, WorkflowID: row.WorkflowID, Name: row.Name, Revision: row.Revision, CreatedAt: row.CreatedAt, Definition: definition}
}

func runFromState(row state.WorkflowRun) Run {
	return Run{ID: row.ID, WorkflowID: row.WorkflowID, VersionID: row.VersionID, State: row.State, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt}
}

func newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:])
}
