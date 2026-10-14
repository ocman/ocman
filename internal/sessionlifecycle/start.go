package sessionlifecycle

import (
	"encoding/json"
	"net/http"
	"os/exec"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
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

// HandleStartSession creates at the chosen target and delivers the first prompt.
func (m *Module) HandleStartSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target
		Worktree bool   `json:"worktree"`
		Title    string `json:"title"`
		// Prompt names automatic worktrees, including client-run commands.
		Prompt  string         `json:"prompt"`
		Send    *startSendBody `json:"send"`
		StartID string         `json:"startId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxSendMessageBody, &req) || !req.Validate(w) {
		return
	}
	var model string
	if req.Send != nil {
		model = req.Send.Model
	}
	if !req.ResolvePlatform(r.Context(), m, w, model) {
		return
	}
	log.WithFields(log.Fields{"platform": req.Platform, "directory": req.Directory, "worktree": req.Worktree}).Info("hub: start session")
	ctx := r.Context()
	permissionRules, err := m.d.PermissionDefaults(ctx, req.Directory, req.RemoteID)
	if err != nil {
		httpx.ServerError(w, "reading project defaults", err)
		return
	}
	if req.StartID != "" {
		ctx = hostsvc.WithProgress(ctx, m.startProgress(req.StartID))
		r = r.WithContext(ctx)
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
			hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepActive)
			hostsvc.ReportProgress(ctx, hostsvc.StepWorktree, hostsvc.StepActive)
		}
		res, err := host.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
			Platform:   base,
			ProjectDir: req.Directory, AutoName: true, Prompt: req.Prompt, Title: req.Title,
			PermissionRules: permissionRules,
		})
		if remoteHost {
			hostsvc.FinishStep(ctx, hostsvc.StepOpencode, err)
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
		hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepActive)
		_, port, ok := m.EnsureProjectForCreate(w, r, resolved, req.Directory)
		if !ok {
			hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepError)
			return
		}
		hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepDone)
		hostsvc.ReportProgress(ctx, hostsvc.StepSession, hostsvc.StepActive)
		resp, err := m.d.Sessions().CreateConfigured(ctx, resolved, platforms.CreateSessionRequest{Directory: req.Directory, Title: req.Title, Port: port}, permissionRules)
		hostsvc.FinishStep(ctx, hostsvc.StepSession, err)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{"platform": resolved, "directory": req.Directory}).Warn("hub: start session failed")
			writeSessionError(w, "creating session", err)
			return
		}
		platform, sessionID, directory = resolved, resp.ID, req.Directory
	}

	sent, sendErr := false, ""
	if req.Send != nil {
		images := make([]platforms.ImageAttachment, 0, len(req.Send.Images))
		for _, img := range req.Send.Images {
			images = append(images, platforms.ImageAttachment{URL: img.URL, Mime: img.Mime})
		}
		hostsvc.ReportProgress(ctx, hostsvc.StepPrompt, hostsvc.StepActive)
		err := m.d.SendNow(ctx, platform, platforms.SendMessageRequest{
			SessionID: sessionID, Message: req.Send.Message, Images: images,
			Model: req.Send.Model, Agent: req.Send.Agent, Reasoning: req.Send.Reasoning,
		})
		hostsvc.FinishStep(ctx, hostsvc.StepPrompt, err)
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

func (m *Module) startProgress(startID string) hostsvc.ProgressFunc {
	return func(step, state string) {
		payload, err := json.Marshal(map[string]string{"startId": startID, "step": step, "state": state})
		if err == nil {
			m.d.Broadcast("ocman.session.start.progress", payload)
		}
	}
}
