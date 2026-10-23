package ocmaint

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/module"
)

// Module owns the /api/maintenance/opencode-db routes and the Runner's
// lifetime. Launch gating stays with the server, which asks LaunchBlocked.
type Module struct {
	runner *Runner // nil when no OpenCode database is configured
	guards module.Guards
}

// NewModule serves r; a nil r answers that maintenance is unavailable.
func NewModule(r *Runner, guards module.Guards) *Module {
	return &Module{runner: r, guards: guards}
}

func (m *Module) Name() string { return "maintenance" }

// Routes checks the method (mux pattern), auth, then localhost for the
// actions, which stop opencode and rewrite its database.
func (m *Module) Routes(r module.Router) {
	auth, local := m.guards.Auth, m.guards.Localhost
	r.Handle("GET /api/maintenance/opencode-db", auth(m.handleStatus))
	r.Handle("POST /api/maintenance/opencode-db/cleanup", auth(local(m.action((*Runner).Cleanup))))
	r.Handle("POST /api/maintenance/opencode-db/restore", auth(local(m.action((*Runner).Restore))))
	r.Handle("POST /api/maintenance/opencode-db/delete-dump", auth(local(m.action((*Runner).DeleteDump))))
}

// LaunchBlocked reports why opencode may not launch right now.
func (m *Module) LaunchBlocked() error {
	if m.runner == nil {
		return nil
	}
	return m.runner.Gate.Err()
}

// Run holds the server's worker join until a running job has finished, so
// the job never outlives the databases its host callbacks write.
func (m *Module) Run(ctx context.Context) {
	<-ctx.Done()
	if m.runner != nil {
		m.runner.Shutdown()
	}
}

type status struct {
	Available  bool   `json:"available"`
	DBPath     string `json:"dbPath,omitempty"`
	DBBytes    int64  `json:"dbBytes"`
	DumpPath   string `json:"dumpPath,omitempty"`
	DumpBytes  int64  `json:"dumpBytes"`
	CutoffDays int    `json:"cutoffDays"`
	Job        Status `json:"job"`
}

func fileBytes(paths ...string) int64 {
	var n int64
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			n += st.Size()
		}
	}
	return n
}

// handleStatus reports the database, the dump, and the job.
func (m *Module) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := status{CutoffDays: int(CutoffAge.Hours() / 24), Job: Status{Steps: []Step{}}}
	if m.runner != nil {
		st.Available = true
		st.DBPath = m.runner.DBPath()
		st.DBBytes = fileBytes(st.DBPath, st.DBPath+"-wal")
		st.DumpPath = m.runner.DumpPath()
		st.DumpBytes = fileBytes(st.DumpPath)
		st.Job = m.runner.Status()
	}
	httpx.WriteJSON(w, st)
}

// action adapts a runner action to a POST answering the new status.
func (m *Module) action(do func(*Runner) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.runner == nil {
			http.Error(w, "OpenCode database maintenance is not available", http.StatusNotFound)
			return
		}
		if err := do(m.runner); err != nil {
			code := http.StatusConflict
			if !errors.Is(err, ErrBusy) && !errors.Is(err, ErrV2) && !errors.Is(err, ErrShutdown) && !errors.Is(err, os.ErrNotExist) {
				code = http.StatusInternalServerError
			}
			http.Error(w, err.Error(), code)
			return
		}
		m.handleStatus(w, r)
	}
}
