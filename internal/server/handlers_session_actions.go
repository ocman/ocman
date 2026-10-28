package server

import (
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/factorymod"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
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
	if !httpx.ReadJSON(w, r, httpx.MaxSendMessageBody, &req) {
		return
	}
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
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
		err = s.enqueueFollowUp(r.Context(), httpx.PlatformHint(r), send)
	} else {
		err = s.sendNow(r.Context(), httpx.PlatformHint(r), send)
	}
	if err != nil {
		writeSessionSvcError(w, "sending message", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionPermission handles POST /api/session/{id}/permissions/{pid}
func (s *Server) handleSessionPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reply string `json:"reply"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	permissionID := r.PathValue("pid")
	if !httpx.ValidID(permissionID) {
		http.Error(w, "invalid permission ID", http.StatusBadRequest)
		return
	}
	if req.Reply != "once" && req.Reply != "always" && req.Reply != "reject" {
		http.Error(w, "invalid reply value: expected once, always, or reject", http.StatusBadRequest)
		return
	}
	factorySession := false
	if req.Reply != "reject" {
		var err error
		factorySession, err = s.factoryModule().Service().IsImplementationSession(r.Context(), sessionID)
		if err != nil {
			factorymod.WriteError(w, err)
			return
		}
	}
	if factorySession {
		adapter, ok := s.registry.PlatformForSession(r.Context(), sessionID)
		if ok {
			if prompts, err := adapter.ListPermissions(r.Context(), sessionID); err == nil {
				for _, prompt := range prompts {
					if id, _ := prompt["id"].(string); id == permissionID {
						gate, handled, err := s.factoryModule().Service().EscalatePermission(r.Context(), sessionID, permissionID, stringValue(prompt["permission"]), strings.Join(autoapprove.PromptPatterns(prompt), ","))
						if err != nil {
							factorymod.WriteError(w, err)
							return
						}
						if handled {
							gate, err = s.factoryModule().Service().ResolveAuthorityEscalationGate(r.Context(), gate.IssueID, "approve")
							if err != nil {
								factorymod.WriteError(w, err)
								return
							}
							httpx.WriteJSON(w, gate)
							return
						}
					}
				}
			}
		}
	}
	// The session service cancels any in-flight auto-approve judge
	// (via the PermissionReplied hook) before forwarding the reply.
	if err := s.sessions.RespondPermission(r.Context(), httpx.PlatformHint(r), platforms.RespondPermissionRequest{
		SessionID:    sessionID,
		PermissionID: permissionID,
		Reply:        req.Reply,
	}); err != nil {
		writeSessionSvcError(w, "responding to permission", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func stringValue(value any) string { result, _ := value.(string); return result }

// handleSessionQuestion dispatches POST /api/session/{id}/questions/{qid}
// and POST /api/session/{id}/questions/{qid}/reject.
func (s *Server) handleSessionQuestion(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	questionID := r.PathValue("qid")
	reject := strings.HasSuffix(r.Pattern, "/reject")
	if !httpx.ValidID(questionID) {
		http.Error(w, "invalid question ID", http.StatusBadRequest)
		return
	}
	if reject {
		if err := s.sessions.RejectQuestion(r.Context(), httpx.PlatformHint(r), platforms.RejectQuestionRequest{
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
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if err := s.sessions.RespondQuestion(r.Context(), httpx.PlatformHint(r), platforms.RespondQuestionRequest{
		SessionID: sessionID,
		RequestID: questionID,
		Answers:   req.Answers,
	}); err != nil {
		writeSessionSvcError(w, "responding to question", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
