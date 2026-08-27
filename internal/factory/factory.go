package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	beadsTimeout   = 5 * time.Second
	beadsStdoutCap = 1 << 20
	beadsStderrCap = 8 << 10
)

type Health string
type Reason string

const (
	HealthHealthy     Health = "healthy"
	HealthUnavailable Health = "unavailable"
	HealthDegraded    Health = "degraded"
)

const (
	ReasonBeadsNotFound            Reason = "beads_not_found"
	ReasonBeadsVersionInvalid      Reason = "beads_version_invalid"
	ReasonBeadsVersionUnsupported  Reason = "beads_version_unsupported"
	ReasonBeadsContractUnsupported Reason = "beads_contract_unsupported"
	ReasonBeadsStoreUnavailable    Reason = "beads_store_unavailable"
	ReasonBeadsCommandFailed       Reason = "beads_command_failed"
	ReasonDispatchLockFailed       Reason = "dispatch_lock_failed"
)

type BeadsHealth struct {
	Usable          bool   `json:"usable"`
	Version         string `json:"version,omitempty"`
	ContractVersion int    `json:"contractVersion,omitempty"`
	Reason          Reason `json:"reason,omitempty"`
	Message         string `json:"message,omitempty"`
}

type Status struct {
	Health        Health      `json:"health"`
	Idle          bool        `json:"idle"`
	DispatchOwner bool        `json:"dispatchOwner"`
	ReadOnly      bool        `json:"readOnly"`
	WorkEpicCount int         `json:"workEpicCount"`
	Beads         BeadsHealth `json:"beads"`
	Reason        Reason      `json:"reason,omitempty"`
	Message       string      `json:"message,omitempty"`
}

type runner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, string, []string, []string) ([]byte, []byte, error)
}

type localExecutionAckStore interface {
	UpsertFactoryLocalExecutionAck(context.Context, string, string, string, string, string, time.Time) error
}

type factoryStore interface {
	localExecutionAckStore
	GetFactoryPlanningSession(context.Context, string) (PlanningSession, bool, error)
	PutFactoryPlanningSession(context.Context, string, string, PlanningSession) error
	AppendFactoryAudit(context.Context, FactoryAuditRecord) error
}

type Service struct {
	dir      string
	runner   runner
	mu       sync.RWMutex
	pourMu   sync.Mutex
	owned    bool
	lockErr  error
	release  func() error
	acks     localExecutionAckStore
	store    factoryStore
	planning PlanningLauncher
}

func New(dir string, ackStore localExecutionAckStore, planning ...PlanningLauncher) *Service {
	svc := newWithRunner(dir, nil, ackStore)
	if len(planning) != 0 {
		svc.planning = planning[0]
	}
	return svc
}

func newWithRunner(dir string, r runner, ackStore localExecutionAckStore) *Service {
	if r == nil {
		r = execRunner{}
	}
	if absolute, err := filepath.Abs(dir); err == nil {
		dir = absolute
	}
	svc := &Service{dir: dir, runner: r, acks: ackStore}
	svc.store, _ = ackStore.(factoryStore)
	return svc
}

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.release != nil || s.owned {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.lockErr = err
		return nil
	}
	release, owned, err := tryLock(filepath.Join(s.dir, "dispatch.lock"))
	s.release, s.owned, s.lockErr = release, owned, err
	if owned {
		initializeBeads(ctx, filepath.Join(s.dir, "beads"), s.runner)
		if s.store != nil && s.planning != nil {
			_ = s.recoverPlanningSessions(ctx)
		}
	}
	return nil
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.release != nil {
		_ = s.release()
	}
	s.release = nil
	s.owned = false
}

func (s *Service) Status(ctx context.Context) Status {
	s.mu.RLock()
	owned, lockErr := s.owned, s.lockErr
	s.mu.RUnlock()

	beads := probeBeads(ctx, filepath.Join(s.dir, "beads"), s.runner)
	status := Status{DispatchOwner: owned, ReadOnly: !owned, Beads: beads}
	if lockErr != nil {
		status.Health = HealthDegraded
		status.Reason = ReasonDispatchLockFailed
		status.Message = "Factory dispatch lock is unavailable; check permissions on the ocman data directory."
		return status
	}
	if !beads.Usable {
		status.Health = HealthUnavailable
		if beads.Reason == ReasonBeadsCommandFailed || beads.Reason == ReasonBeadsStoreUnavailable {
			status.Health = HealthDegraded
		}
		status.Reason, status.Message = beads.Reason, beads.Message
		return status
	}
	status.Health = HealthHealthy
	status.Idle = true
	path, err := s.runner.LookPath("bd")
	if err != nil {
		status.Health = HealthDegraded
		status.Idle = false
		status.Reason = ReasonBeadsCommandFailed
		status.Message = "Factory work could not be listed; verify the Beads installation."
		return status
	}
	epics, err := listWorkEpics(ctx, path, filepath.Join(s.dir, "beads"), s.runner)
	if err != nil {
		status.Health = HealthDegraded
		status.Idle = false
		status.Reason = ReasonBeadsCommandFailed
		status.Message = "Factory work could not be listed; verify the Beads store."
		return status
	}
	status.WorkEpicCount = len(epics)
	return status
}

func probeBeads(parent context.Context, dir string, r runner) BeadsHealth {
	if !filepath.IsAbs(dir) {
		return BeadsHealth{Reason: ReasonBeadsStoreUnavailable, Message: "Factory Beads directory must be an absolute path."}
	}
	path, version, failure := compatibleBeads(parent, dir, r)
	if failure.Reason != "" {
		return failure
	}

	statusOut, err := run(parent, r, path, parentDir(dir), []string{"--readonly", "status", "--no-activity", "--json"}, beadsCommandEnv(dir))
	if err != nil {
		return BeadsHealth{Version: version, Reason: ReasonBeadsStoreUnavailable, Message: "Factory Beads store is unavailable; verify its data directory and run bd status."}
	}
	contract, ok := parseContract(statusOut)
	if !ok {
		return BeadsHealth{Version: version, Reason: ReasonBeadsContractUnsupported, Message: "Beads returned an unsupported JSON contract; ocman requires contract 1."}
	}
	if contract != 1 {
		return BeadsHealth{Version: version, Reason: ReasonBeadsContractUnsupported, Message: fmt.Sprintf("Beads JSON contract %d is unsupported; ocman requires contract 1.", contract)}
	}
	return BeadsHealth{Usable: true, Version: version, ContractVersion: contract}
}

func initializeBeads(parent context.Context, dir string, r runner) {
	path, _, failure := compatibleBeads(parent, dir, r)
	if failure.Reason != "" {
		return
	}
	_, _ = run(parent, r, path, parentDir(dir), []string{
		"init", "--quiet", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--init-if-missing",
	}, beadsCommandEnv(dir))
}

func compatibleBeads(parent context.Context, dir string, r runner) (string, string, BeadsHealth) {
	path, err := r.LookPath("bd")
	if err != nil {
		return "", "", BeadsHealth{Reason: ReasonBeadsNotFound, Message: "Beads is not installed; install bd version >=1.1.0 and <1.2.0."}
	}
	versionOut, err := run(parent, r, path, parentDir(dir), []string{"version", "--json"}, beadsCommandEnv(dir))
	if err != nil {
		return "", "", BeadsHealth{Reason: ReasonBeadsCommandFailed, Message: "Beads version check failed; verify that bd can run as the ocman user."}
	}
	version, valid := parseVersion(versionOut)
	if !valid {
		return "", "", BeadsHealth{Reason: ReasonBeadsVersionInvalid, Message: "Beads returned an invalid version; ocman requires version >=1.1.0 and <1.2.0."}
	}
	if !strings.HasPrefix(version, "1.1.") {
		return "", "", BeadsHealth{Reason: ReasonBeadsVersionUnsupported, Message: fmt.Sprintf("Beads %s is unsupported; install version >=1.1.0 and <1.2.0.", version)}
	}
	return path, version, BeadsHealth{}
}

func parentDir(path string) string {
	parent := filepath.Dir(path)
	if parent == "." {
		return ""
	}
	return parent
}

func parseVersion(data []byte) (string, bool) {
	var out struct {
		SchemaVersion int `json:"schema_version"`
		Data          *struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if !decodeOne(data, &out) || out.SchemaVersion != 1 || out.Data == nil {
		return "", false
	}
	parts := strings.Split(out.Data.Version, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return "", false
		}
	}
	return out.Data.Version, true
}

func beadsCommandEnv(dir string) []string {
	return []string{
		"BD_JSON_ENVELOPE=1", "BEADS_DIR=" + dir, "BEADS_DB=", "BD_DB=", "BD_NON_INTERACTIVE=1", "BEADS_ACTOR=ocman-factory",
	}
}

func parseContract(data []byte) (int, bool) {
	var out struct {
		SchemaVersion int `json:"schema_version"`
		Data          *struct {
			Summary *struct {
				TotalIssues *int `json:"total_issues"`
			} `json:"summary"`
		} `json:"data"`
	}
	if !decodeOne(data, &out) || out.Data == nil || out.Data.Summary == nil || out.Data.Summary.TotalIssues == nil {
		return out.SchemaVersion, false
	}
	return out.SchemaVersion, true
}

func decodeOne(data []byte, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func run(parent context.Context, r runner, path, dir string, args, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, beadsTimeout)
	defer cancel()
	out, _, err := r.Run(ctx, path, dir, args, env)
	return out, err
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (execRunner) Run(ctx context.Context, path, dir string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	stdout := limitedBuffer{remaining: beadsStdoutCap}
	stderr := limitedBuffer{remaining: beadsStderrCap}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		err = errors.New("beads output exceeded limit")
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type limitedBuffer struct {
	bytes.Buffer
	remaining int
	overflow  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.remaining {
		_, _ = b.Buffer.Write(p[:b.remaining])
		b.remaining = 0
		b.overflow = true
		return n, nil
	}
	b.remaining -= n
	_, _ = b.Buffer.Write(p)
	return n, nil
}
