package sessionlifecycle

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

func platformHint(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("platform"))
}

func writeSessionError(w http.ResponseWriter, msg string, err error) {
	var ve *sessionsvc.ValidationError
	if errors.As(err, &ve) {
		http.Error(w, ve.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, sessionsvc.ErrNoPlatformAvailable) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	httpx.WritePlatformError(w, msg, err)
}

func (m *Module) HandleSessionCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command   string `json:"command"`
		Arguments string `json:"arguments"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Reasoning string `json:"reasoning"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().ExecuteCommand(r.Context(), platformHint(r), platforms.ExecuteCommandRequest{
			SessionID: sessionID,
			Command:   req.Command,
			Arguments: req.Arguments,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
		}); err != nil {
			writeSessionError(w, "executing command", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// HandleSessionShell runs a raw shell command in the session's working directory.
func (m *Module) HandleSessionShell(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
		Agent   string `json:"agent"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().RunShell(r.Context(), platformHint(r), platforms.RunShellRequest{
			SessionID: sessionID,
			Command:   req.Command,
			Agent:     req.Agent,
		}); err != nil {
			writeSessionError(w, "running shell command", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *Module) HandleSessionRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().Rename(r.Context(), platformHint(r), platforms.RenameSessionRequest{
			SessionID: sessionID,
			Title:     req.Title,
		}); err != nil {
			writeSessionError(w, "renaming session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *Module) HandleSessionAbort(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		// A client dropping the connection must not cancel the stop it asked for.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
		defer cancel()
		if err := m.d.Sessions().Abort(ctx, platformHint(r), platforms.AbortRequest{SessionID: sessionID}); err != nil {
			writeSessionError(w, "aborting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *Module) HandleSessionRevert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"messageID"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().Revert(r.Context(), platformHint(r), platforms.RevertSessionRequest{SessionID: sessionID, MessageID: req.MessageID}); err != nil {
			writeSessionError(w, "reverting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *Module) HandleSessionUnrevert(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().Unrevert(r.Context(), platformHint(r), platforms.UnrevertSessionRequest{SessionID: sessionID}); err != nil {
			writeSessionError(w, "restoring session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *Module) HandleSessionCompact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().Compact(r.Context(), platformHint(r), platforms.CompactRequest{
			SessionID:  sessionID,
			ProviderID: req.ProviderID,
			ModelID:    req.ModelID,
		}); err != nil {
			writeSessionError(w, "compacting session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// HandleSessionFork branches a session, optionally from a specific message.
func (m *Module) HandleSessionFork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"messageID"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		resp, err := m.d.Sessions().Fork(r.Context(), platformHint(r), platforms.ForkSessionRequest{
			SessionID: sessionID,
			MessageID: req.MessageID,
		})
		if err != nil {
			writeSessionError(w, "forking session", err)
			return
		}
		httpx.WriteJSON(w, map[string]string{"id": resp.ID})
	})
}

// HandleSessionMove relocates a session to another directory on the same host.
func (m *Module) HandleSessionMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	m.d.WithSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		if err := m.d.Sessions().Move(r.Context(), platformHint(r), platforms.MoveSessionRequest{
			SessionID: sessionID,
			Directory: req.Directory,
		}); err != nil {
			writeSessionError(w, "moving session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
