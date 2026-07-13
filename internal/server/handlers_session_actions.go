package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/tmux"
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
		// Queue, when true, tells the server this send was made while the
		// agent was mid-turn and must be QUEUED — never drained into the
		// running turn. The client knows this authoritatively from the
		// live SSE stream (isRunning); the server's own status inference
		// reads the lagging DB and can wrongly report idle, which would
		// send the message immediately (#58). This flag is the fix.
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
		// Composer sends always enqueue (#58). When the client marks the
		// send as queued (agent mid-turn), the server holds it for the
		// next session.idle edge. Otherwise the enqueue fast-path drains
		// it immediately if the session is idle. Enqueue validates
		// message-or-images.
		if err := s.queueSvc().Enqueue(r.Context(), platformHint(r), req.Queue, platforms.SendMessageRequest{
			SessionID: sessionID,
			Message:   req.Message,
			Images:    images,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
		}); err != nil {
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
	if !isLoopback(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !tmux.IsAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
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
		target, err := tmux.RestartOpencode(detail.Session.Directory)
		if err != nil {
			if errors.Is(err, tmux.ErrNoManagedOpencodePane) {
				http.Error(w, "no tmux-managed OpenCode pane found for this session", http.StatusConflict)
				return
			}
			serverError(w, "restarting opencode", err)
			return
		}
		writeJSON(w, map[string]string{"target": target})
	})
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
