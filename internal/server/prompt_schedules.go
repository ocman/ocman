package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

const promptScheduleTickInterval = 5 * time.Second

type managedPromptSessions struct{ server *Server }

func (m managedPromptSessions) CreateScheduledSession(ctx context.Context, remoteID, directory string) (string, *platforms.CreateSessionResponse, error) {
	host := m.server.router().ForRemote(remoteID)
	ensured, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: directory})
	if err != nil {
		return "", nil, err
	}
	platformID := "opencode"
	if remoteID := host.RemoteID(); remoteID != "" && remoteID != "local" {
		platformID = remote.CompoundPlatformID(remoteID, platformID)
	}
	resp, err := m.server.sessions.Create(ctx, platformID, platforms.CreateSessionRequest{Directory: directory, Port: ensured.Port()})
	return platformID, resp, err
}

func (m managedPromptSessions) SendScheduledMessage(ctx context.Context, platformID string, req platforms.SendMessageRequest, queue bool) error {
	if queue {
		return m.server.queueSvc().Enqueue(ctx, platformID, false, req)
	}
	return m.server.sessions.SendMessage(ctx, platformID, req)
}

func (s *Server) handlePromptSchedules(w http.ResponseWriter, r *http.Request) {
	if s.promptScheduleSvc == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/prompt-schedules"), "/")
	if rest == "" {
		s.handlePromptScheduleCollection(w, r)
		return
	}
	id, action, extra := cutWorkflowPath(rest)
	if id == "" || extra != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet && action == "" {
		schedule, err := s.promptScheduleSvc.Get(r.Context(), id)
		if errors.Is(err, state.ErrPromptScheduleNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if err != nil {
			serverError(w, "getting prompt schedule", err)
			return
		}
		writeJSON(w, schedule)
		return
	}
	if r.Method != http.MethodPost || (action != "cancel" && action != "run-now" && action != "enable" && action != "disable") {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var schedule state.PromptSchedule
	var err error
	switch action {
	case "cancel":
		schedule, err = s.promptScheduleSvc.Cancel(r.Context(), id)
	case "enable", "disable":
		schedule, err = s.promptScheduleSvc.SetEnabled(r.Context(), id, action == "enable")
	default:
		schedule, err = s.promptScheduleSvc.RunNow(r.Context(), id)
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, state.ErrPromptScheduleNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrInvalidState) {
			status = http.StatusConflict
		}
		if status == http.StatusInternalServerError {
			serverError(w, "updating prompt schedule", err)
		} else {
			http.Error(w, err.Error(), status)
		}
		return
	}
	writeJSON(w, schedule)
}

func (s *Server) handlePromptScheduleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		schedules, err := s.promptScheduleSvc.List(r.Context(), r.URL.Query().Get("directory"), r.URL.Query().Get("remoteId"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrValidation) {
				status = http.StatusBadRequest
			}
			if status == http.StatusInternalServerError {
				serverError(w, "listing prompt schedules", err)
			} else {
				http.Error(w, err.Error(), status)
			}
			return
		}
		if schedules == nil {
			schedules = []state.PromptSchedule{}
		}
		writeJSON(w, schedules)
	case http.MethodPost:
		var req promptScheduleCreateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		schedule, err := s.promptScheduleSvc.Create(r.Context(), req)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrValidation) {
				status = http.StatusBadRequest
			}
			if status == http.StatusInternalServerError {
				serverError(w, "creating prompt schedule", err)
			} else {
				http.Error(w, err.Error(), status)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, schedule)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) runPromptSchedules(ctx context.Context) {
	if s.promptScheduleSvc == nil {
		return
	}
	ticker := time.NewTicker(promptScheduleTickInterval)
	defer ticker.Stop()
	for {
		if err := s.promptScheduleSvc.Tick(ctx); err != nil {
			log.WithError(err).Warn("prompt-schedules: tick")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
