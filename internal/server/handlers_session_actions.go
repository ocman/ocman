package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// --- Session-scoped mutating endpoints ---

func (s *Server) handleSessionMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
		Images  []struct {
			URL  string `json:"url"`
			Mime string `json:"mime"`
		} `json:"images"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Reasoning string `json:"reasoning"`
		// Queue, when true, holds the message in the follow-up queue for
		// the session's next idle edge instead of delivering it now. The
		// client sets it from an explicit user gesture (Ctrl/Cmd+Enter in
		// the composer), never from inferred status — the server's own
		// inference reads the lagging DB and can't be trusted for this
		// (#58).
		//
		// When false the message is sent straight through, even mid-turn:
		// OpenCode interleaves it into the running turn, which is the
		// point of a plain Enter send.
		Queue bool `json:"queue"`
	}
	if !readAndUnmarshal(w, r, maxSendMessageBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		images := make([]platforms.ImageAttachment, 0, len(req.Images))
		for _, img := range req.Images {
			images = append(images, platforms.ImageAttachment{URL: img.URL, Mime: img.Mime})
		}
		send := platforms.SendMessageRequest{
			SessionID: sessionID,
			Message:   req.Message,
			Images:    images,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
		}
		// Queue explicitly requested (#58): hold for the next idle edge.
		// Otherwise deliver now — mid-turn included, so a plain Enter
		// send is picked up by the running turn instead of waiting for
		// it to finish. Both paths validate message-or-images.
		var err error
		if req.Queue {
			err = s.queueSvc().Enqueue(r.Context(), platformHint(r), true, send)
		} else {
			err = s.sendNow(r.Context(), platformHint(r), send)
		}
		if err != nil {
			writeSessionSvcError(w, "sending message", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command   string `json:"command"`
		Arguments string `json:"arguments"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Reasoning string `json:"reasoning"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.ExecuteCommand(r.Context(), platformHint(r), platforms.ExecuteCommandRequest{
			SessionID: sessionID,
			Command:   req.Command,
			Arguments: req.Arguments,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
		}); err != nil {
			writeSessionSvcError(w, "executing command", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionRestartOpencode(w http.ResponseWriter, r *http.Request) {
	if !s.isPrivilegedRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	all := r.URL.Query().Get("all") == "true"
	force := r.URL.Query().Get("force") == "true"
	confirmed := r.URL.Query().Get("confirmed") == "true"
	if r.URL.Query().Get("all") != "" && !all || r.URL.Query().Get("force") != "" && !force || r.URL.Query().Get("confirmed") != "" && !confirmed {
		http.Error(w, "invalid restart options", http.StatusBadRequest)
		return
	}
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			writePlatformError(w, "loading session", err)
			return
		}
		if detail == nil || detail.Session == nil || detail.Session.Directory == "" {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		targets, err := s.restartTargets(r.Context(), detail.Session.Directory, detail.Session.RemoteID, all)
		if err != nil {
			serverError(w, "listing managed opencode", err)
			return
		}
		if len(targets) == 0 {
			http.Error(w, "no managed OpenCode instance found", http.StatusNotFound)
			return
		}
		busy, err := s.waitForRestartIdle(r.Context(), targets, force)
		if err != nil {
			serverError(w, "checking OpenCode sessions", err)
			return
		}
		if force && !confirmed && len(busy) > 0 {
			writeJSON(w, map[string]any{"confirmationRequired": true, "busySessions": busy})
			return
		}
		for _, target := range targets {
			if _, err := target.host.RestartProjectOpencode(r.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: target.root}); err != nil {
				serverError(w, "restarting opencode", err)
				return
			}
		}
		writeJSON(w, map[string]any{"restarted": len(targets)})
	})
}

type restartTarget struct {
	host hostsvc.Host
	root string
}

func (s *Server) restartTargets(ctx context.Context, dir, remoteID string, all bool) ([]restartTarget, error) {
	var hosts []hostsvc.Host
	if all {
		hosts = append([]hostsvc.Host{s.router().Local()}, mapsValues(s.router().Remotes())...)
	} else if remoteID != "" && remoteID != "local" {
		host, ok := s.router().LookupRemote(remoteID)
		if !ok {
			return nil, fmt.Errorf("remote host %q is unavailable", remoteID)
		}
		hosts = []hostsvc.Host{host}
	} else {
		hosts = []hostsvc.Host{s.router().ForDir(dir)}
	}
	seen := make(map[string]bool)
	var targets []restartTarget
	for _, host := range hosts {
		if seen[host.RemoteID()] {
			continue
		}
		seen[host.RemoteID()] = true
		instances, err := host.ManagedOpencodes(ctx)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if all || sameProject(instance.RepoRoot, dir) {
				targets = append(targets, restartTarget{host: host, root: instance.RepoRoot})
			}
		}
	}
	return targets, nil
}

func (s *Server) waitForRestartIdle(ctx context.Context, targets []restartTarget, force bool) ([]string, error) {
	for {
		busy, err := s.restartBusySessions(ctx, targets)
		if err != nil || force || len(busy) == 0 {
			return busy, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) restartBusySessions(ctx context.Context, targets []restartTarget) ([]string, error) {
	var busy []string
	for _, adapter := range s.registry.Platforms() {
		sessions, err := adapter.Sessions(ctx, "", 0)
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			if session.Status != db.StatusBusy {
				continue
			}
			owner := session.RemoteID
			if owner == "" {
				owner = "local"
			}
			for _, target := range targets {
				if target.host.RemoteID() != owner || !sameProject(target.root, session.Directory) {
					continue
				}
				busy = append(busy, session.ID)
				break
			}
		}
	}
	return busy, nil
}

func sameProject(root, dir string) bool {
	return pathContains(root, dir) || pathContains(filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root)), dir)
}

func pathContains(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mapsValues(m map[string]hostsvc.Host) []hostsvc.Host {
	out := make([]hostsvc.Host, 0, len(m))
	for _, host := range m {
		out = append(out, host)
	}
	return out
}

// handleSessionShell handles POST /api/session/{id}/shell — runs a
// raw shell command in the session's working directory, bypassing the LLM.
func (s *Server) handleSessionShell(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
		Agent   string `json:"agent"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.RunShell(r.Context(), platformHint(r), platforms.RunShellRequest{
			SessionID: sessionID,
			Command:   req.Command,
			Agent:     req.Agent,
		}); err != nil {
			writeSessionSvcError(w, "running shell command", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.Rename(r.Context(), platformHint(r), platforms.RenameSessionRequest{
			SessionID: sessionID,
			Title:     req.Title,
		}); err != nil {
			writeSessionSvcError(w, "renaming session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionAbort(w http.ResponseWriter, r *http.Request) {
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.Abort(r.Context(), platformHint(r), platforms.AbortRequest{SessionID: sessionID}); err != nil {
			writeSessionSvcError(w, "aborting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionRevert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"messageID"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.Revert(r.Context(), platformHint(r), platforms.RevertSessionRequest{SessionID: sessionID, MessageID: req.MessageID}); err != nil {
			writeSessionSvcError(w, "reverting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionUnrevert(w http.ResponseWriter, r *http.Request) {
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.Unrevert(r.Context(), platformHint(r), platforms.UnrevertSessionRequest{SessionID: sessionID}); err != nil {
			writeSessionSvcError(w, "restoring session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.Compact(r.Context(), platformHint(r), platforms.CompactRequest{
			SessionID:  sessionID,
			ProviderID: req.ProviderID,
			ModelID:    req.ModelID,
		}); err != nil {
			writeSessionSvcError(w, "compacting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionFork handles POST /api/session/{id}/fork: branches the
// session into a new child session, optionally from a specific message.
// Returns {"id": "<new session id>"}.
func (s *Server) handleSessionFork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"messageID"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		resp, err := s.sessions.Fork(r.Context(), platformHint(r), platforms.ForkSessionRequest{
			SessionID: sessionID,
			MessageID: req.MessageID,
		})
		if err != nil {
			writeSessionSvcError(w, "forking session", err)
			return
		}
		writeJSON(w, map[string]string{"id": resp.ID})
	})
}

// handleSessionMove handles POST /api/session/{id}/move: relocates the
// session to another project directory on the same host.
func (s *Server) handleSessionMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.Move(r.Context(), platformHint(r), platforms.MoveSessionRequest{
			SessionID: sessionID,
			Directory: req.Directory,
		}); err != nil {
			writeSessionSvcError(w, "moving session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionPermissionRulesGet handles GET /api/session/{id}/permission-rules.
// Returns {"rules":[...]}; an empty list means the session inherits the
// platform's configured defaults.
func (s *Server) handleSessionPermissionRulesGet(w http.ResponseWriter, r *http.Request) {
	s.withSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string, adapter platforms.Platform) {
		rules, err := adapter.PermissionRules(r.Context(), sessionID)
		if err != nil {
			writePlatformError(w, "reading permission rules", err)
			return
		}
		if rules == nil {
			rules = []platforms.PermissionRule{}
		}
		writeJSON(w, map[string]interface{}{"rules": rules})
	})
}

// handleSessionPermissionRulesSet handles PUT /api/session/{id}/permission-rules.
// Body: {"rules":[{"permission","pattern","action"}...]}. An empty list
// restores the platform's configured defaults.
func (s *Server) handleSessionPermissionRulesSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []platforms.PermissionRule `json:"rules"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := s.sessions.SetPermissionRules(r.Context(), platformHint(r), platforms.SetPermissionRulesRequest{
			SessionID: sessionID,
			Rules:     req.Rules,
		}); err != nil {
			writeSessionSvcError(w, "setting permission rules", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionPermission handles POST /api/session/{id}/permissions/{pid}
func (s *Server) handleSessionPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reply string `json:"reply"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string) {
		permissionID := strings.TrimPrefix(rest, "permissions/")
		if !validateID(permissionID) {
			http.Error(w, "invalid permission ID", http.StatusBadRequest)
			return
		}
		// The session service cancels any in-flight auto-approve judge
		// (via the PermissionReplied hook) before forwarding the reply.
		if err := s.sessions.RespondPermission(r.Context(), platformHint(r), platforms.RespondPermissionRequest{
			SessionID:    sessionID,
			PermissionID: permissionID,
			Reply:        req.Reply,
		}); err != nil {
			writeSessionSvcError(w, "responding to permission", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionQuestion dispatches POST /api/session/{id}/questions/{qid}
// and POST /api/session/{id}/questions/{qid}/reject.
func (s *Server) handleSessionQuestion(w http.ResponseWriter, r *http.Request) {
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string) {
		rest = strings.TrimPrefix(rest, "questions/")
		questionID := rest
		reject := false
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			questionID = rest[:slash]
			if rest[slash+1:] == "reject" {
				reject = true
			} else {
				http.Error(w, "unknown question subpath", http.StatusNotFound)
				return
			}
		}
		if !validateID(questionID) {
			http.Error(w, "invalid question ID", http.StatusBadRequest)
			return
		}
		if reject {
			if err := s.sessions.RejectQuestion(r.Context(), platformHint(r), platforms.RejectQuestionRequest{
				SessionID: sessionID,
				RequestID: questionID,
			}); err != nil {
				writeSessionSvcError(w, "rejecting question", err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var req struct {
			Answers [][]string `json:"answers"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &req) {
			return
		}
		if err := s.sessions.RespondQuestion(r.Context(), platformHint(r), platforms.RespondQuestionRequest{
			SessionID: sessionID,
			RequestID: questionID,
			Answers:   req.Answers,
		}); err != nil {
			writeSessionSvcError(w, "responding to question", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
