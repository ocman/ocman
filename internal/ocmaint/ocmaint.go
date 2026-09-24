// Package ocmaint runs maintenance on OpenCode's SQLite database: it
// moves the per-file patches OpenCode keeps in summary.diffs out of old
// sessions into a restorable dump, then compacts the database. Every job
// stops ocman's managed opencode instances first, refuses to touch the
// database while any other process holds it, and relaunches the stopped
// instances afterwards.
package ocmaint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// CutoffAge is how old a session's last update must be before its diffs
// are removed.
const CutoffAge = 30 * 24 * time.Hour

// ErrBusy is returned when a job is already running.
var ErrBusy = errors.New("maintenance already running")

// Deps are the host operations a job needs. All are required except
// OnChanged.
type Deps struct {
	DBPath   string
	DumpPath string
	// Instances lists the repo roots of managed opencode instances.
	Instances func(context.Context) ([]string, error)
	Stop      func(ctx context.Context, repoRoot string) error
	Start     func(ctx context.Context, repoRoot string) error
	// Holders lists processes other than ocman holding the database.
	Holders   func(ctx context.Context, dbPath string) ([]Holder, error)
	FreeBytes func(dir string) (uint64, error)
	// OnChanged runs after the database was modified.
	OnChanged func()
	// HolderWait bounds how long stopped instances get to release the
	// database. Zero means 15s.
	HolderWait time.Duration
}

// Step is one visible stage of a job.
type Step struct {
	Name   string `json:"name"`
	State  string `json:"state"` // pending, running, done, failed, skipped
	Detail string `json:"detail,omitempty"`
}

// Status is a snapshot of the current or last job.
type Status struct {
	Job        string    `json:"job,omitempty"` // cleanup or restore
	Running    bool      `json:"running"`
	Steps      []Step    `json:"steps"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// Runner runs at most one job at a time. Its Gate is closed while a job
// runs so nothing launches opencode mid-job.
type Runner struct {
	deps Deps
	Gate Gate

	mu     sync.Mutex
	status Status
}

func New(deps Deps) *Runner {
	if deps.HolderWait == 0 {
		deps.HolderWait = 15 * time.Second
	}
	return &Runner{deps: deps, status: Status{Steps: []Step{}}}
}

// DBPath and DumpPath expose the configured files.
func (r *Runner) DBPath() string   { return r.deps.DBPath }
func (r *Runner) DumpPath() string { return r.deps.DumpPath }

// Status returns a copy of the job state.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	s.Steps = append([]Step{}, s.Steps...) // never nil: encodes as [], not null
	return s
}

// Cleanup starts the diff-removal job in the background.
func (r *Runner) Cleanup() error {
	return r.start("cleanup", []string{"Check disk space", "Stop opencode", "Back up database", "Dump diffs", "Remove diffs", "Compact database", "Delete backup", "Relaunch opencode"}, r.cleanup)
}

// Restore starts putting the dumped diffs back in the background.
func (r *Runner) Restore() error {
	if _, err := os.Stat(r.deps.DumpPath); err != nil {
		return fmt.Errorf("no dump to restore: %w", err)
	}
	return r.start("restore", []string{"Check disk space", "Stop opencode", "Restore diffs", "Relaunch opencode"}, r.restore)
}

// DeleteDump removes the dump file.
func (r *Runner) DeleteDump() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Running {
		return ErrBusy
	}
	for _, p := range []string{r.deps.DumpPath, r.deps.DumpPath + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (r *Runner) start(job string, names []string, body func(context.Context, *jobRun) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Running {
		return ErrBusy
	}
	steps := make([]Step, len(names))
	for i, n := range names {
		steps[i] = Step{Name: n, State: "pending"}
	}
	r.status = Status{Job: job, Running: true, Steps: steps, StartedAt: time.Now()}
	r.Gate.close("OpenCode database maintenance is running")
	go r.run(body)
	return nil
}

func (r *Runner) run(body func(context.Context, *jobRun) error) {
	ctx := context.Background()
	j := &jobRun{r: r}
	err := body(ctx, j)
	r.Gate.open()
	// Relaunch is always the last step and runs even after a failure.
	if relaunchErr := j.step("Relaunch opencode", func() (string, error) { return j.relaunch(ctx) }); err == nil {
		err = relaunchErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Running = false
	r.status.FinishedAt = time.Now()
	if err != nil {
		r.status.Error = err.Error()
		for i := range r.status.Steps {
			if r.status.Steps[i].State == "pending" && r.status.Steps[i].Name != "Relaunch opencode" {
				r.status.Steps[i].State = "skipped"
			}
		}
		log.WithError(err).Warn("ocmaint: job failed")
	}
}

// jobRun carries one job's state between steps.
type jobRun struct {
	r       *Runner
	stopped []string
}

// step runs fn as the named step, recording its outcome.
func (j *jobRun) step(name string, fn func() (string, error)) error {
	j.set(name, "running", "")
	detail, err := fn()
	if err != nil {
		j.set(name, "failed", err.Error())
		return err
	}
	j.set(name, "done", detail)
	return nil
}

func (j *jobRun) set(name, state, detail string) {
	j.r.mu.Lock()
	defer j.r.mu.Unlock()
	for i := range j.r.status.Steps {
		if j.r.status.Steps[i].Name == name {
			j.r.status.Steps[i].State = state
			j.r.status.Steps[i].Detail = detail
		}
	}
}

// stopAll stops every managed instance, then waits for all other holders
// to go. It fails, listing them, if any remain.
func (j *jobRun) stopAll(ctx context.Context) (string, error) {
	d := j.r.deps
	roots, err := d.Instances(ctx)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if err := d.Stop(ctx, root); err != nil {
			return "", fmt.Errorf("stopping %s: %w", root, err)
		}
		j.stopped = append(j.stopped, root)
	}
	deadline := time.Now().Add(d.HolderWait)
	for {
		holders, err := d.Holders(ctx, d.DBPath)
		if err != nil {
			return "", err
		}
		if len(holders) == 0 {
			return fmt.Sprintf("stopped %d managed instance(s)", len(roots)), nil
		}
		if time.Now().After(deadline) {
			names := make([]string, len(holders))
			for i, h := range holders {
				names[i] = h.String()
			}
			return "", fmt.Errorf("close these processes first; they hold the OpenCode database: %s", strings.Join(names, ", "))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (j *jobRun) relaunch(ctx context.Context) (string, error) {
	var failed []string
	for _, root := range j.stopped {
		if err := j.r.deps.Start(ctx, root); err != nil {
			log.WithError(err).WithField("repoRoot", root).Warn("ocmaint: relaunching opencode")
			failed = append(failed, root)
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("could not relaunch: %s", strings.Join(failed, ", "))
	}
	return fmt.Sprintf("relaunched %d instance(s)", len(j.stopped)), nil
}

func (j *jobRun) changed() {
	if j.r.deps.OnChanged != nil {
		j.r.deps.OnChanged()
	}
}
