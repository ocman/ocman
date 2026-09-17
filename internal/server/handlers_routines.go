package server

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/webhook"
)

type routineRequest struct {
	Name               string                   `json:"name"`
	Prompt             string                   `json:"prompt"`
	Directory          string                   `json:"directory"`
	RemoteID           string                   `json:"remoteId"`
	Agent              string                   `json:"agent"`
	Model              string                   `json:"model"`
	SessionMode        string                   `json:"sessionMode"`
	SessionID          string                   `json:"sessionId"`
	Schedule           routineScheduleRequest   `json:"schedule"`
	Enabled            bool                     `json:"enabled"`
	DeleteAfterSuccess bool                     `json:"deleteAfterSuccess"`
	PermissionRules    []platforms.PermissionRule `json:"permissionRules"`
}

type routineScheduleRequest struct {
	Kind      string `json:"kind"`
	TimeoutMS int64  `json:"timeoutMs"`
	At        int64  `json:"at"`
	Cron      string `json:"cron"`
	Timezone  string `json:"timezone"`
}

func (req routineRequest) input() (routines.Input, error) {
	if req.Schedule.TimeoutMS > math.MaxInt64/int64(time.Millisecond) || req.Schedule.TimeoutMS < math.MinInt64/int64(time.Millisecond) {
		return routines.Input{}, routines.ErrValidation
	}
	return routines.Input{
		Name: req.Name, Prompt: req.Prompt, Directory: req.Directory, RemoteID: req.RemoteID, Agent: req.Agent, Model: req.Model, SessionMode: req.SessionMode, SessionID: req.SessionID,
		Schedule: routines.Schedule{
			Kind: req.Schedule.Kind, Timeout: time.Duration(req.Schedule.TimeoutMS) * time.Millisecond,
			At: time.UnixMilli(req.Schedule.At), Cron: req.Schedule.Cron, Timezone: req.Schedule.Timezone,
		},
		Enabled: req.Enabled, DeleteAfterSuccess: req.DeleteAfterSuccess,
		PermissionRules: req.PermissionRules,
	}, nil
}

func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	if s.routineSvc == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/routines"), "/")
	if rest == "" {
		s.handleRoutineCollection(w, r)
		return
	}
	id, action, extra := cutRoutinePath(rest)
	if id == "" || (extra != "" && (action != "webhook-inbox" || extra != "subscriptions")) {
		http.NotFound(w, r)
		return
	}

	switch action {
	case "":
		s.handleRoutineItem(w, r, id)
	case "run":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		run, err := s.routineSvc.RunNow(r.Context(), id)
		if err != nil {
			s.writeRoutineError(w, "running routine", err)
			return
		}
		writeJSON(w, run)
	case "history":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		runs, err := s.routineSvc.History(r.Context(), id)
		if err != nil {
			s.writeRoutineError(w, "listing routine history", err)
			return
		}
		if runs == nil {
			runs = []state.RoutineRun{}
		}
		writeJSON(w, runs)
	case "webhook-inbox":
		if extra == "subscriptions" {
			s.handleWebhookSubscriptions(w, r, id)
		} else {
			s.handleWebhookInbox(w, r, id)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWebhookSubscriptions(w http.ResponseWriter, r *http.Request, routineID string) {
	inbox, err := s.stateDB.GetWebhookInbox(r.Context(), routineID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		subs, err := s.stateDB.ListWebhookSubscriptions(r.Context(), inbox.ID)
		if err != nil {
			serverError(w, "listing webhook subscriptions", err)
			return
		}
		writeJSON(w, subs)
	case http.MethodPut:
		var sub state.WebhookSubscription
		if !readAndUnmarshal(w, r, maxRequestBody, &sub) {
			return
		}
		sub.InboxID, sub.RoutineID = inbox.ID, routineID
		if sub.ID == "" {
			sub.ID = "subscription-" + inbox.ID + "-" + sub.RoutineID
		}
		if err := s.stateDB.SaveWebhookSubscription(r.Context(), sub); err != nil {
			serverError(w, "saving webhook subscription", err)
			return
		}
		writeJSON(w, sub)
	case http.MethodDelete:
		var sub struct {
			RoutineID string `json:"routineId"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &sub) {
			return
		}
		if err := s.stateDB.DeleteWebhookSubscription(r.Context(), inbox.ID, sub.RoutineID); err != nil {
			serverError(w, "deleting webhook subscription", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type webhookInboxView struct {
	ID            string                      `json:"id"`
	RoutineID     string                      `json:"routineId"`
	RelayURL      string                      `json:"relayUrl"`
	IngestionURL  string                      `json:"ingestionUrl"`
	KeyVersion    int                         `json:"keyVersion"`
	CreatedAt     int64                       `json:"createdAt"`
	Counts        map[string]int              `json:"counts"`
	Subscriptions []state.WebhookSubscription `json:"subscriptions"`
}

func (s *Server) handleWebhookInbox(w http.ResponseWriter, r *http.Request, routineID string) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	routine, err := s.routineSvc.Get(r.Context(), routineID)
	if err != nil {
		s.writeRoutineError(w, "getting routine", err)
		return
	}
	if routine.RemoteID != "" && routine.RemoteID != "local" {
		if r.Method != http.MethodPost || s.remotes == nil {
			http.Error(w, "webhook inbox owner is unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct{ EnrollmentToken, Secret, SecretHeader string }
		if !readAndUnmarshal(w, r, maxRequestBody, &req) {
			return
		}
		if s.relayURL == "" {
			http.Error(w, "relay is not configured", http.StatusServiceUnavailable)
			return
		}
		inbox, err := s.remotes.RegisterWebhookInboxWithSecret(r.Context(), routine.RemoteID, routine.ID, s.relayURL, req.EnrollmentToken, req.Secret, req.SecretHeader)
		if err != nil {
			serverError(w, "registering remote webhook inbox", err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, struct {
			webhookInboxView
			ValidationSecret string `json:"validationSecret,omitempty"`
			ValidationHeader string `json:"validationHeader,omitempty"`
		}{webhookInboxView{inbox.ID, routine.ID, inbox.RelayURL, inbox.IngestionURL, inbox.KeyVersion, inbox.CreatedAt, map[string]int{}, []state.WebhookSubscription{}}, req.Secret, req.SecretHeader})
		return
	}
	inbox, err := s.stateDB.GetWebhookInbox(r.Context(), routineID)
	switch r.Method {
	case http.MethodGet:
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, nil)
				return
			}
			serverError(w, "getting webhook inbox", err)
			return
		}
		counts, err := s.stateDB.WebhookDispatchCounts(r.Context(), inbox.ID)
		if err != nil {
			serverError(w, "getting webhook status", err)
			return
		}
		subs, err := s.stateDB.ListWebhookSubscriptions(r.Context(), inbox.ID)
		if err != nil {
			serverError(w, "getting webhook subscriptions", err)
			return
		}
		writeJSON(w, webhookInboxView{inbox.ID, routine.ID, inbox.RelayURL, inbox.IngestionURL, inbox.KeyVersion, inbox.CreatedAt, counts, subs})
	case http.MethodPost:
		if err == nil {
			http.Error(w, "webhook inbox already exists", http.StatusConflict)
			return
		}
		var req struct{ EnrollmentToken, Secret, SecretHeader string }
		if !readAndUnmarshal(w, r, maxRequestBody, &req) {
			return
		}
		if s.relayURL == "" {
			http.Error(w, "relay is not configured", http.StatusServiceUnavailable)
			return
		}
		inbox, err := webhook.RegisterWithSecret(r.Context(), s.stateDB, routine.ID, s.relayURL, req.EnrollmentToken, req.Secret, req.SecretHeader, nil)
		if err != nil {
			serverError(w, "registering webhook inbox", err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, struct {
			webhookInboxView
			ValidationSecret string `json:"validationSecret,omitempty"`
			ValidationHeader string `json:"validationHeader,omitempty"`
		}{webhookInboxView{inbox.ID, routine.ID, inbox.RelayURL, inbox.IngestionURL, inbox.KeyVersion, inbox.CreatedAt, map[string]int{}, []state.WebhookSubscription{}}, req.Secret, req.SecretHeader})
	case http.MethodDelete:
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := (&share.RelayClient{BaseURL: inbox.RelayURL}).RevokeInbox(r.Context(), inbox.ID, inbox.ManagementToken); err != nil {
			serverError(w, "revoking webhook inbox", err)
			return
		}
		if err := s.stateDB.DeleteWebhookInbox(r.Context(), routineID); err != nil {
			serverError(w, "deleting webhook inbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Recipient string `json:"recipient"`
			Reset     bool   `json:"reset"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &req) {
			return
		}
		if req.Reset {
			req.Recipient = ""
		}
		if req.Recipient == "" {
			id, e := age.GenerateX25519Identity()
			if e != nil {
				serverError(w, "generating webhook key", e)
				return
			}
			req.Recipient = id.Recipient().String()
			inbox.Identity = id.String()
		}
		version, err := (&share.RelayClient{BaseURL: inbox.RelayURL}).RotateInbox(r.Context(), inbox.ID, inbox.ManagementToken, req.Recipient)
		if err != nil {
			serverError(w, "rotating webhook inbox", err)
			return
		}
		inbox.KeyVersion = version
		if err := s.stateDB.SaveWebhookInbox(r.Context(), inbox); err != nil {
			serverError(w, "saving webhook key", err)
			return
		}
		writeJSON(w, struct {
			KeyVersion int  `json:"keyVersion"`
			Reset      bool `json:"reset"`
		}{version, req.Reset})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func cutRoutinePath(path string) (first, second, rest string) {
	parts := strings.SplitN(path, "/", 3)
	first = parts[0]
	if len(parts) > 1 {
		second = parts[1]
	}
	if len(parts) > 2 {
		rest = parts[2]
	}
	return
}

func (s *Server) handleRoutineCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.routineSvc.List(r.Context(), false)
		if err != nil {
			s.writeRoutineError(w, "listing routines", err)
			return
		}
		if items == nil {
			items = []state.Routine{}
		}
		writeJSON(w, items)
	case http.MethodPost:
		input, ok := readRoutineInput(w, r)
		if !ok {
			return
		}
		item, err := s.routineSvc.Create(r.Context(), input)
		if err != nil {
			s.writeRoutineError(w, "creating routine", err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, item)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRoutineItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		item, err := s.routineSvc.Get(r.Context(), id)
		if err != nil {
			s.writeRoutineError(w, "getting routine", err)
			return
		}
		writeJSON(w, item)
	case http.MethodPut:
		input, ok := readRoutineInput(w, r)
		if !ok {
			return
		}
		item, err := s.routineSvc.Update(r.Context(), id, input)
		if err != nil {
			s.writeRoutineError(w, "updating routine", err)
			return
		}
		writeJSON(w, item)
	case http.MethodDelete:
		if err := s.routineSvc.Delete(r.Context(), id); err != nil {
			s.writeRoutineError(w, "deleting routine", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func readRoutineInput(w http.ResponseWriter, r *http.Request) (routines.Input, bool) {
	var req routineRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return routines.Input{}, false
	}
	input, err := req.input()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return routines.Input{}, false
	}
	return input, true
}

func (s *Server) writeRoutineError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, routines.ErrValidation):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, state.ErrRoutineNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, routines.ErrNameConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, state.ErrRoutineRunActive):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		serverError(w, operation, err)
	}
}
