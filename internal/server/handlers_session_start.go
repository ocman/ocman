package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
)

// A new conversation has no session until its first prompt. The composer
// is served by /api/sessions/prepare (catalogs for a directory) and the
// first submission by /api/sessions/start, which creates the session at
// the chosen target (worktree or current checkout) and sends the prompt
// in one request. Nothing is created, archived or re-homed before that.

// ensureProjectForCreate makes sure the project's opencode instance is
// running on the platform's owning host before a session is created there.
//
// Adapter-side port discovery only *finds* an instance, so an instance
// killed outside ocman left every subsequent create failing with "no
// running OpenCode instance for directory" — ensure is what relaunches
// it. The owner comes from the compound platform id; a platform without
// one is the hub's own adapter, so the ensure is pinned to the local host
// and never ForDir inference, which could map the directory to a remote
// (the same absolute path can exist on an attached machine) and launch
// opencode there while Create targets the hub.
//
// A remote ensure failure is fatal: the remote has no discovery fallback,
// so Create would fail anyway. A local ensure failure is soft — a non-repo
// directory (or a host that can't launch) can't be ensured, but discovery
// may still find a usable instance. Returns ok=false after writing the
// HTTP error.
// resolveDraftOwner checks that the platform's owner is connected and the
// platform known, writing the rejection otherwise.
func (s *Server) resolveDraftOwner(w http.ResponseWriter, platform, directory string) bool {
	_, ok := s.draftOwner(w, platform, directory)
	return ok
}

func (s *Server) draftOwner(w http.ResponseWriter, platform, directory string) (hostsvc.Host, bool) {
	remoteID, _ := remote.SplitPlatformID(platform)
	owner := remoteID
	if owner == "" {
		owner = "local"
	}
	host, ok := s.resolveOwner(w, directory, owner)
	if !ok {
		return nil, false
	}
	// Validate the platform before any side effect (#533): an unknown
	// platform must not launch a managed opencode instance only for
	// Create to reject the request afterwards.
	if !s.sessions.KnownPlatform(platform) {
		http.Error(w, "unknown platform", http.StatusBadRequest)
		return nil, false
	}
	return host, true
}

func (s *Server) ensureProjectForCreate(w http.ResponseWriter, r *http.Request, platform, directory string) (hostsvc.Host, string, bool) {
	remoteID, _ := remote.SplitPlatformID(platform)
	host, ok := s.draftOwner(w, platform, directory)
	if !ok {
		return nil, "", false
	}
	// A worktree runs on the project's shared instance rooted at the main
	// checkout; fold the path back so ensuring a worktree directory can't
	// launch a second instance for the same project.
	ensured, err := host.EnsureProjectOpencode(r.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: projectRootForDirectory(directory)})
	fields := log.Fields{"platform": platform, "directory": directory}
	switch {
	case err != nil && remoteID != "":
		log.WithError(err).WithFields(fields).Warn("hub: ensure project opencode failed")
		writeSessionSvcError(w, "creating session", err)
		return nil, "", false
	case err != nil:
		log.WithError(err).WithFields(fields).Debug("hub: ensure project opencode failed; falling back to port discovery")
		return host, "", true
	default:
		return host, ensured.Port(), true
	}
}

type newSessionTarget struct {
	Platform  string `json:"platform"`
	Directory string `json:"directory"`
	RemoteID  string `json:"remoteId"`
}

// An explicit owner is authoritative, even when both machines share a path.
// Older callers may omit it and retain the platform's owner (or local).
func (t *newSessionTarget) resolvePlatform(s *Server, w http.ResponseWriter) bool {
	if t.RemoteID == "" {
		t.RemoteID = remoteIDForPlatform(t.Platform)
	}
	if t.Platform != "" && remoteIDForPlatform(t.Platform) != t.RemoteID {
		http.Error(w, "platform does not belong to remoteId", http.StatusBadRequest)
		return false
	}
	host, ok := s.resolveOwner(w, t.Directory, t.RemoteID)
	if !ok {
		return false
	}
	if t.Platform == "" {
		t.Platform = opencodePlatformForHost(host)
	}
	return true
}

func (t newSessionTarget) validate(w http.ResponseWriter) bool {
	if t.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return false
	}
	if !filepath.IsAbs(t.Directory) {
		http.Error(w, "directory must be an absolute path", http.StatusBadRequest)
		return false
	}
	return true
}

// handlePrepareSession ensures the directory's instance is up and returns
// the composer catalog for it. POST because the ensure is a side effect.
func (s *Server) handlePrepareSession(w http.ResponseWriter, r *http.Request) {
	var req newSessionTarget
	if !readAndUnmarshal(w, r, maxRequestBody, &req) || !req.validate(w) {
		return
	}
	if !req.resolvePlatform(s, w) {
		return
	}
	// Opening the composer or picking a machine must not launch OpenCode:
	// the catalogs come from an instance that is already running, else the
	// historical models. The first submission (/start) launches it.
	if !s.resolveDraftOwner(w, req.Platform, req.Directory) {
		return
	}
	catalog, platformID, err := s.sessions.DirectoryCatalog(r.Context(), req.Platform, platforms.DirectoryCatalogRequest{Directory: req.Directory})
	if err != nil {
		writeSessionSvcError(w, "preparing session", err)
		return
	}
	if catalog == nil {
		catalog = &platforms.DirectoryCatalog{}
	}
	if catalog.Agents == nil {
		catalog.Agents = []platforms.AgentCatalogEntry{}
	}
	if catalog.Commands == nil {
		catalog.Commands = []platforms.SlashCommandEntry{}
	}
	if catalog.Models == nil {
		catalog.Models = &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}
	}
	if s.sessions != nil {
		for i := range catalog.Models.Models {
			if t := s.sessions.CooldownUntil(catalog.Models.Models[i].Provider); !t.IsZero() {
				catalog.Models.Models[i].CooldownUntil = &t
			}
		}
	}
	writeJSON(w, map[string]any{
		"platform":            platformID,
		"agents":              catalog.Agents,
		"commands":            catalog.Commands,
		"models":              catalog.Models,
		"defaultAgent":        catalog.DefaultAgent,
		"defaultModel":        catalog.DefaultModel,
		"projectDefaultModel": s.projectDefaultModel(r.Context(), req.Directory),
		"liveConnection":      catalog.LiveConnection,
	})
}

// startSendBody is the first prompt, delivered server-side so the client
// needs no second round trip. Failures are reported, never fatal to the
// start: the session exists and the client keeps the draft.
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

// handleStartSession creates the session at its target and sends the first
// prompt. Worktree targets create `session-<id8>` on the owner (named in
// the background from Prompt); current-checkout targets create in
// Directory on the project's instance.
func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		newSessionTarget
		Worktree bool   `json:"worktree"`
		Title    string `json:"title"`
		// Prompt names an automatic worktree; it is the first submission's
		// text even when that submission is a command the client runs itself.
		Prompt string         `json:"prompt"`
		Send   *startSendBody `json:"send"`
		// StartID, when set, tags ocman.session.start.progress broadcasts
		// so the submitting client can show each step as it happens.
		StartID string `json:"startId"`
	}
	// The first message may carry image attachments.
	if !readAndUnmarshal(w, r, maxSendMessageBody, &req) || !req.validate(w) {
		return
	}
	if !req.resolvePlatform(s, w) {
		return
	}
	log.WithFields(log.Fields{"platform": req.Platform, "directory": req.Directory, "worktree": req.Worktree}).Info("hub: start session")
	ctx := r.Context()
	defaults, err := s.getProjectDefaults(ctx, req.Directory, req.RemoteID)
	if err != nil {
		serverError(w, "reading project defaults", err)
		return
	}
	var permissionRules []platforms.PermissionRule
	if defaults != nil {
		permissionRules, err = defaults.permissionRules()
		if err != nil {
			serverError(w, "reading default permission mode", err)
			return
		}
	}
	if req.StartID != "" {
		ctx = hostsvc.WithProgress(ctx, s.startProgress(req.StartID))
		r = r.WithContext(ctx)
	}

	var (
		platform, sessionID, directory, worktreePath, branch string
	)
	if req.Worktree {
		remoteID, _ := remote.SplitPlatformID(req.Platform)
		if remoteID == "" {
			// git is a host-local precondition; a remote validates its own.
			if _, err := exec.LookPath("git"); err != nil {
				http.Error(w, "git is not available", http.StatusServiceUnavailable)
				return
			}
			remoteID = "local"
		}
		host, ok := s.resolveOwner(w, req.Directory, remoteID)
		if !ok {
			return
		}
		if !s.sessions.KnownPlatform(req.Platform) {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return
		}
		// Progress does not cross the gRPC seam: report a remote's parallel
		// checkout and instance around the call; the local host reports its own.
		remoteHost := remoteID != "local"
		if remoteHost {
			hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepActive)
			hostsvc.ReportProgress(ctx, hostsvc.StepWorktree, hostsvc.StepActive)
		}
		res, err := host.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
			ProjectDir: req.Directory, AutoName: true, Prompt: req.Prompt, Title: req.Title,
			PermissionRules: permissionRules,
		})
		if remoteHost {
			hostsvc.FinishStep(ctx, hostsvc.StepOpencode, err)
			hostsvc.FinishStep(ctx, hostsvc.StepWorktree, err)
		}
		if err != nil {
			writeWorktreeCreateError(w, err)
			return
		}
		platform, sessionID, directory, worktreePath, branch = opencodePlatformForHost(host), res.SessionID, res.WorktreePath, res.WorktreePath, res.Branch
	} else {
		resolved, err := s.sessions.ResolvePlatformID(r.Context(), req.Platform)
		if err != nil {
			writeSessionSvcError(w, "creating session", err)
			return
		}
		hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepActive)
		_, port, ok := s.ensureProjectForCreate(w, r, resolved, req.Directory)
		if !ok {
			hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepError)
			return
		}
		hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepDone)
		hostsvc.ReportProgress(ctx, hostsvc.StepSession, hostsvc.StepActive)
		resp, err := s.sessions.CreateConfigured(ctx, resolved, platforms.CreateSessionRequest{Directory: req.Directory, Title: req.Title, Port: port}, permissionRules)
		hostsvc.FinishStep(ctx, hostsvc.StepSession, err)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{"platform": resolved, "directory": req.Directory}).Warn("hub: start session failed")
			writeSessionSvcError(w, "creating session", err)
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
		err := s.sendNow(ctx, platform, platforms.SendMessageRequest{
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
	writeJSON(w, map[string]any{
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

// startProgress broadcasts a new conversation's start steps to every client;
// only the one holding startId renders them.
func (s *Server) startProgress(startID string) hostsvc.ProgressFunc {
	return func(step, state string) {
		payload, err := json.Marshal(map[string]string{"startId": startID, "step": step, "state": state})
		if err == nil {
			s.broadcastGlobalEvent("ocman.session.start.progress", payload)
		}
	}
}

func remoteIDForPlatform(platform string) string {
	if remoteID, _ := remote.SplitPlatformID(platform); remoteID != "" {
		return remoteID
	}
	return "local"
}

// writeWorktreeCreateError maps CreateWorktreeSession failures to HTTP.
func writeWorktreeCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, git.ErrNotARepo):
		http.Error(w, "projectDir is not a git repository", http.StatusNotFound)
	case errors.Is(err, git.ErrBranchCheckedOutElsewhere), errors.Is(err, git.ErrPathConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.WithError(err).Warn("worktree: create and launch")
		http.Error(w, "worktree create/launch failed: "+err.Error(), http.StatusBadGateway)
	}
}
