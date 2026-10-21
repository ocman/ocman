package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/worker"
)

type sessionJob struct {
	pluginID string
	request  plugins.Request
}

// SessionBroker is the grant-scoped seam between a session.v1 plugin and
// ocman's session orchestration.
func (m *Module) SessionBroker() *plugins.SessionBroker {
	m.sessionOnce.Do(m.buildSessions)
	return m.sessionBroker
}

func (m *Module) sessionWorker() *worker.Worker[sessionJob] {
	m.sessionOnce.Do(m.buildSessions)
	return m.sessionJobs
}

func (m *Module) sessionDeliveryWorker() *worker.Worker[PluginSessionCompletion] {
	m.sessionOnce.Do(m.buildSessions)
	return m.sessionDeliveries
}

func (m *Module) buildSessions() {
	m.sessionBroker = plugins.NewSessionBroker(
		func(ctx context.Context, id string, use func(plugins.Description, []string, plugins.SessionConfig) error) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.store == nil || m.stopping {
				return plugins.ErrUnavailable
			}
			return m.store.WithPluginConversation(ctx, id, func(d plugins.Description, grants []string, cfg plugins.ConversationConfig) error {
				return use(d, grants, plugins.SessionConfig{Project: cfg.Project, Values: cfg.Values}) //nolint:staticcheck // S1016 would hide field drift
			})
		}, m.startSession,
		func(ctx context.Context, id string, call plugins.Call) (<-chan plugins.Reply, error) {
			p := m.processes[id]
			if p == nil {
				return nil, plugins.ErrUnavailable
			}
			return p.Call(ctx, call)
		})
	m.sessionBroker.ResolveProject = m.deps.ProjectDirectory
	m.sessionJobs = worker.NewKeyed(m.runSessionJob, func(j sessionJob) string { return j.pluginID })
	m.sessionDeliveries = worker.NewKeyed(func(c PluginSessionCompletion) {
		runWithRecover("plugin-session-delivery", func() { m.DeliverSessionCompletion(c) })
	}, PluginSessionCompletion.Group)
	m.sessionInFlight = make(map[int64]bool)
}

func (m *Module) runSessionJob(job sessionJob) {
	r := job.request
	result, err := m.SessionBroker().Run(r.Context, job.pluginID, r.Call)
	value, _ := json.Marshal(result)
	r.Respond(value, err)
}

// sessionDirectory resolves symlinks before the first session side effect.
// The resolved directory, rather than the plugin's symlink path, is passed on.
func sessionDirectory(project, subpath string) (string, bool) {
	directory, ok := plugins.SessionDirectoryFor(project, subpath)
	if !ok || !filepath.IsAbs(project) {
		return "", false
	}
	root, err := filepath.EvalSymlinks(project)
	if err != nil {
		return "", false
	}
	dir, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(dir)
	return dir, err == nil && info.IsDir()
}

func (m *Module) startSession(ctx context.Context, pluginID string, config plugins.SessionConfig, params plugins.SessionRunParams, directory, operationID string) (string, error) {
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	if !m.workers.Go(func(lifetime context.Context) {
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		if lifetime.Err() != nil {
			cancel()
		}
		id, err := m.startSessionRun(callCtx, pluginID, config, params, directory, operationID)
		done <- result{id, err}
	}) {
		return "", plugins.ErrUnavailable
	}
	select {
	case r := <-done:
		return r.id, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (m *Module) startSessionRun(ctx context.Context, pluginID string, config plugins.SessionConfig, params plugins.SessionRunParams, directory, operationID string) (sessionID string, runErr error) {
	dir, ok := sessionDirectory(config.Project, params.Subpath)
	if !ok {
		return "", &plugins.WireError{Category: plugins.ErrorPermissionDenied}
	}
	if m.store == nil {
		return "", plugins.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, claimed, err := m.plugins.reserveSessionRun(ctx, pluginID, operationID, config.Project, params)
	if err != nil || !claimed {
		if err != nil {
			return id, err
		}
		return m.pluginWireSession(ctx, "opencode", id)
	}
	defer func() {
		if err := m.plugins.finishSessionRun(ctx, pluginID, operationID, sessionID, runErr); err != nil {
			sessionID, runErr = "", err
		}
	}()
	if err := m.plugins.PluginSessionBacklogFull(ctx, pluginID); err != nil {
		return "", plugins.ErrUnavailable
	}
	var rules []platforms.PermissionRule
	if params.PermissionProfile != "" {
		resolved, err := m.plugins.ResolvePermissionProfile(ctx, params.PermissionProfile)
		if errors.Is(err, ErrPermissionProfileNotFound) {
			return "", &plugins.WireError{Category: plugins.ErrorNotFound}
		}
		if err != nil {
			return "", plugins.ErrUnavailable
		}
		rules = resolved
	}
	if params.NoTools {
		rules = []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "deny"}}
	}
	host := m.deps.Local()
	const platformID = "opencode"
	ensured, err := host.EnsureRuntime(ctx, platformID, filepath.Clean(config.Project))
	if err != nil {
		return "", err
	}
	created, err := m.deps.Sessions.CreateConfigured(ctx, platformID, platforms.CreateSessionRequest{
		Directory: dir, Title: "plugin:" + pluginID + ":" + operationID, Port: ensured.Port(),
	}, rules)
	if err != nil {
		return "", err
	}
	if err := m.plugins.LinkPluginSession(ctx, platformID, created.ID, pluginID); err != nil {
		log.WithError(err).WithFields(log.Fields{"plugin_id": pluginID, "session_id": created.ID}).
			Error("linking plugin session; its completed turns will not be delivered")
		return "", err
	}
	if err := m.deps.Enqueue(ctx, platformID, false, platforms.SendMessageRequest{
		SessionID: created.ID, Message: params.Prompt, Agent: params.Agent, Model: params.Model,
	}); err != nil {
		return "", err
	}
	return created.ID, nil
}
