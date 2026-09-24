package server

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocmaint"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// WithOpenCodeDBPath enables OpenCode database maintenance for the
// database at path. Must be called before Start.
func (s *Server) WithOpenCodeDBPath(path string) *Server {
	if path == "" {
		return s
	}
	local := func() hostsvc.Host { return s.router().Local() }
	s.maint = ocmaint.New(ocmaint.Deps{
		DBPath:   path,
		DumpPath: path + ".ocman-diffs",
		Instances: func(ctx context.Context) ([]string, error) {
			managed, err := local().ManagedOpencodes(ctx)
			roots := make([]string, len(managed))
			for i, m := range managed {
				roots[i] = m.RepoRoot
			}
			return roots, err
		},
		Stop: func(ctx context.Context, root string) error {
			return local().StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root})
		},
		Start: func(ctx context.Context, root string) error {
			_, err := local().EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root})
			return err
		},
		Holders:   ocmaint.LsofHolders,
		FreeBytes: ocmaint.FreeBytes,
		OnChanged: opencode.InvalidateSessionsCache,
	})
	return s
}

// launchBlocked reports why opencode may not launch right now.
func (s *Server) launchBlocked() error {
	if s.maint == nil {
		return nil
	}
	return s.maint.Gate.Err()
}

// gatedRuntime refuses launches while maintenance runs; probe and stop
// pass through so the job itself can stop instances.
type gatedRuntime struct {
	ocruntime.Runtime
	blocked func() error
}

func (g gatedRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	if err := g.blocked(); err != nil {
		return nil, err
	}
	return g.Runtime.Launch(ctx, spec)
}

func (s *Server) gatedRuntime() ocruntime.Runtime {
	rt := s.runtime
	if rt == nil {
		rt = ocruntime.NewNativeRuntime()
	}
	return gatedRuntime{Runtime: rt, blocked: s.launchBlocked}
}

func (s *Server) gateLaunchTmux(launch func(context.Context, string) (string, error)) func(context.Context, string) (string, error) {
	return func(ctx context.Context, dir string) (string, error) {
		if err := s.launchBlocked(); err != nil {
			return "", err
		}
		return launch(ctx, dir)
	}
}

type maintenanceStatus struct {
	Available  bool           `json:"available"`
	DBPath     string         `json:"dbPath,omitempty"`
	DBBytes    int64          `json:"dbBytes"`
	DumpPath   string         `json:"dumpPath,omitempty"`
	DumpBytes  int64          `json:"dumpBytes"`
	CutoffDays int            `json:"cutoffDays"`
	Job        ocmaint.Status `json:"job"`
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

// handleMaintenanceStatus reports the database, the dump, and the job.
func (s *Server) handleMaintenanceStatus(w http.ResponseWriter, _ *http.Request) {
	st := maintenanceStatus{CutoffDays: int(ocmaint.CutoffAge.Hours() / 24), Job: ocmaint.Status{Steps: []ocmaint.Step{}}}
	if s.maint != nil {
		st.Available = true
		st.DBPath = s.maint.DBPath()
		st.DBBytes = fileBytes(st.DBPath, st.DBPath+"-wal")
		st.DumpPath = s.maint.DumpPath()
		st.DumpBytes = fileBytes(st.DumpPath)
		st.Job = s.maint.Status()
	}
	writeJSON(w, st)
}

// maintenanceAction adapts a runner action to a localhost-only POST.
func (s *Server) maintenanceAction(action func(*ocmaint.Runner) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.maint == nil {
			http.Error(w, "OpenCode database maintenance is not available", http.StatusNotFound)
			return
		}
		if err := action(s.maint); err != nil {
			status := http.StatusConflict
			if !errors.Is(err, ocmaint.ErrBusy) && !errors.Is(err, os.ErrNotExist) {
				status = http.StatusInternalServerError
			}
			http.Error(w, err.Error(), status)
			return
		}
		s.handleMaintenanceStatus(w, r)
	}
}
