package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// startSendBody is delivered in the start request. Failures leave the created
// session available for the client's retry controls.
type startSendBody struct {
	Message string `json:"message"`
	Images  []struct {
		URL  string `json:"url"`
		Mime string `json:"mime"`
	} `json:"images"`
	Model     string `json:"model"`
	Agent     string `json:"agent"`
	Reasoning string `json:"reasoning"`
}

type startRequest struct {
	Target
	Worktree bool           `json:"worktree"`
	Title    string         `json:"title"`
	Prompt   string         `json:"prompt"`
	Send     *startSendBody `json:"send"`
	StartID  string         `json:"startId"`
	DraftID  string         `json:"draftId,omitempty"`
	// PermissionRules, when present, replaces the owner/project defaults with
	// the posture chosen in the composer before the first prompt.
	PermissionRules *[]platforms.PermissionRule `json:"permissionRules,omitempty"`
	Command         *struct {
		Command   string `json:"command"`
		Arguments string `json:"arguments"`
	} `json:"command,omitempty"`
	Shell *struct {
		Command string `json:"command"`
	} `json:"shell,omitempty"`
}

// HandleStartSession creates at the chosen target and delivers the first prompt.
func (m *Module) HandleStartSession(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if !httpx.ReadJSON(w, r, httpx.MaxSendMessageBody, &req) || !req.Validate(w) {
		return
	}
	if req.DraftID != "" && !sessioncatalog.IsID(req.DraftID) || req.Command != nil && req.Shell != nil {
		http.Error(w, "invalid draft launch", http.StatusBadRequest)
		return
	}
	if !validStartRules(w, req.PermissionRules) {
		return
	}
	var model string
	if req.Send != nil {
		model = req.Send.Model
	}
	if !req.ResolvePlatform(r.Context(), m, w, model) {
		return
	}
	var creation *platforms.CreationIdentity
	if req.DraftID != "" {
		creation = &platforms.CreationIdentity{SessionID: req.DraftID, RequestKey: "draft:" + req.DraftID, Origin: "user"}
	}
	if req.Send != nil {
		if creation == nil {
			creation = &platforms.CreationIdentity{Origin: "user"}
		}
		creation.Agent, creation.Model, creation.Reasoning = req.Send.Agent, req.Send.Model, req.Send.Reasoning
	}
	log.WithFields(log.Fields{"platform": req.Platform, "directory": req.Directory, "worktree": req.Worktree}).Info("hub: start session")
	ctx := r.Context()
	permissionRules, err := m.startPermissionRules(ctx, req)
	if err != nil {
		httpx.ServerError(w, "reading project defaults", err)
		return
	}
	if req.StartID != "" {
		ctx = hostsvc.WithProgress(ctx, m.startProgress(ctx, req.StartID))
		r = r.WithContext(ctx)
	}

	if !m.cloneForStart(w, r, req.Target) {
		return
	}
	var platform, sessionID, directory, worktreePath, branch string
	if req.Worktree {
		remoteID, base := platforms.SplitPlatformID(req.Platform)
		if remoteID == "" {
			// git is a host-local precondition; a remote validates its own.
			if _, err := exec.LookPath("git"); err != nil {
				http.Error(w, "git is not available", http.StatusServiceUnavailable)
				return
			}
			remoteID = "local"
		}
		host, ok := m.d.Owner(w, req.Directory, remoteID)
		if !ok {
			return
		}
		if !m.d.Sessions().KnownPlatform(req.Platform) {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return
		}
		// Progress does not cross gRPC; the hub reports the remote call's steps.
		remoteHost := remoteID != "local"
		if remoteHost {
			hostsvc.ReportProgress(ctx, hostsvc.StepPlatform, hostsvc.StepActive)
			hostsvc.ReportProgress(ctx, hostsvc.StepWorktree, hostsvc.StepActive)
		}
		res, err := host.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
			Creation:   creation,
			Platform:   base,
			ProjectDir: req.Directory, AutoName: true, Prompt: req.Prompt, Title: req.Title,
			PermissionRules: permissionRules,
		})
		if remoteHost {
			hostsvc.FinishStep(ctx, hostsvc.StepPlatform, err)
			hostsvc.FinishStep(ctx, hostsvc.StepWorktree, err)
		}
		if err != nil {
			httpx.WriteCreateError(w, err)
			return
		}
		platform, sessionID, directory, worktreePath, branch = req.Platform, res.SessionID, res.WorktreePath, res.WorktreePath, res.Branch
	} else {
		resolved, err := m.d.Sessions().ResolvePlatformID(ctx, req.Platform)
		if err != nil {
			writeSessionError(w, "creating session", err)
			return
		}
		hostsvc.ReportProgress(ctx, hostsvc.StepPlatform, hostsvc.StepActive)
		_, port, ok := m.EnsureProjectForCreate(w, r, resolved, req.Directory)
		if !ok {
			hostsvc.ReportProgress(ctx, hostsvc.StepPlatform, hostsvc.StepError)
			return
		}
		hostsvc.ReportProgress(ctx, hostsvc.StepPlatform, hostsvc.StepDone)
		hostsvc.ReportProgress(ctx, hostsvc.StepSession, hostsvc.StepActive)
		resp, err := m.d.Sessions().CreateConfigured(ctx, resolved, platforms.CreateSessionRequest{Directory: req.Directory, Title: req.Title, Port: port, Creation: creation}, permissionRules)
		hostsvc.FinishStep(ctx, hostsvc.StepSession, err)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{"platform": resolved, "directory": req.Directory}).Warn("hub: start session failed")
			writeSessionError(w, "creating session", err)
			return
		}
		platform, sessionID, directory = resolved, resp.ID, req.Directory
	}

	if req.DraftID != "" {
		if sessionID != req.DraftID {
			http.Error(w, "The owning machine did not preserve the draft identity. Update ocman on that machine, then discard this failed draft and start a new conversation. Setup has not been retried.", http.StatusBadGateway)
			return
		}
		if m.d.DB != nil {
			write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err := (draftStore{m.d.DB}).confirmed(write, sessionID, map[string]any{"sessionId": sessionID, "platform": platform, "remoteId": remoteIDForPlatform(platform), "directory": directory, "worktreePath": worktreePath, "branch": branch, "firstMessageSent": false})
			cancel()
			if err != nil {
				httpx.ServerError(w, "recording session binding", err)
				return
			}
		}
	}
	sent, sendErr := false, ""
	if req.Send != nil {
		err := m.deliverFirst(ctx, req, platform, sessionID)
		if err != nil {
			log.WithError(err).Warn("hub: sending first message")
			sendErr = err.Error()
		} else {
			sent = true
		}
	}
	httpx.WriteJSON(w, map[string]any{
		"sessionId":         sessionID,
		"platform":          platform,
		"remoteId":          remoteIDForPlatform(platform),
		"directory":         directory,
		"worktreePath":      worktreePath,
		"branch":            branch,
		"firstMessageSent":  sent,
		"firstMessageError": sendErr,
	})
}

func (m *Module) startProgress(ctx context.Context, startID string) hostsvc.ProgressFunc {
	return func(step, state string) {
		if m.d.DB != nil {
			if err := (draftStore{m.d.DB}).progress(ctx, startID, step, state); err != nil {
				log.WithError(err).Warn("persisting setup progress")
			}
		}
		payload, err := json.Marshal(map[string]string{"startId": startID, "step": step, "state": state})
		if err == nil {
			m.d.Broadcast("ocman.session.start.progress", payload)
		}
	}
}

// startPermissionRules prefers the composer's explicit posture over defaults.
func (m *Module) startPermissionRules(ctx context.Context, req startRequest) ([]platforms.PermissionRule, error) {
	if req.PermissionRules != nil {
		return *req.PermissionRules, nil
	}
	return m.d.PermissionDefaults(ctx, req.Directory, req.RemoteID)
}

// validStartRules rejects malformed rules before any worktree or session exists.
func validStartRules(w http.ResponseWriter, rules *[]platforms.PermissionRule) bool {
	if rules == nil {
		return true
	}
	for _, rule := range *rules {
		switch rule.Action {
		case "allow", "deny", "ask":
		default:
			http.Error(w, "invalid permission rule action: expected allow, deny, or ask", http.StatusBadRequest)
			return false
		}
		if rule.Permission == "" {
			http.Error(w, "permission rule permission is required", http.StatusBadRequest)
			return false
		}
	}
	return true
}
