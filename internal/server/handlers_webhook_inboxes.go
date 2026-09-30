package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/webhook"
	log "github.com/sirupsen/logrus"
)

// webhookDeliveryLogSize bounds the debugging log; bodies can be large.
const webhookDeliveryLogSize = 10

// webhookInboxView is an inbox without its relay credentials or private key.
// Secret is the user's own shared secret, shown back so it can be checked.
type webhookInboxView struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name"`
	RelayURL      string                      `json:"relayUrl"`
	IngestionURL  string                      `json:"ingestionUrl"`
	KeyVersion    int                         `json:"keyVersion"`
	CreatedAt     int64                       `json:"createdAt"`
	SecretHeader  string                      `json:"secretHeader"`
	Secret        string                      `json:"secret"`
	Counts        map[string]int              `json:"counts"`
	Subscriptions []state.WebhookSubscription `json:"subscriptions"`
}

func (s *Server) webhookInboxView(r *http.Request, inbox state.WebhookInbox) (webhookInboxView, error) {
	counts, err := s.stateDB.WebhookDispatchCounts(r.Context(), inbox.ID)
	if err != nil {
		return webhookInboxView{}, err
	}
	subs, err := s.stateDB.ListWebhookSubscriptions(r.Context(), inbox.ID)
	if err != nil {
		return webhookInboxView{}, err
	}
	if subs == nil {
		subs = []state.WebhookSubscription{}
	}
	return webhookInboxView{inbox.ID, inbox.Name, inbox.RelayURL, inbox.IngestionURL, inbox.KeyVersion, inbox.CreatedAt, inbox.SecretHeader, inbox.Secret, counts, subs}, nil
}

// handleWebhookInboxes serves /api/webhook-inboxes[/{id}[/deliveries|/subscriptions]].
// Inboxes only capture deliveries; routines subscribe to them with filters.
func (s *Server) handleWebhookInboxes(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil || s.routineSvc == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	id, action, extra := cutRoutinePath(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/webhook-inboxes"), "/"))
	if id == "" {
		s.handleWebhookInboxCollection(w, r)
		return
	}
	if extra != "" {
		http.NotFound(w, r)
		return
	}
	inbox, err := s.stateDB.GetWebhookInboxByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		serverError(w, "getting webhook inbox", err)
		return
	}
	switch action {
	case "":
		s.handleWebhookInboxItem(w, r, inbox)
	case "deliveries":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		deliveries, err := s.stateDB.ListWebhookDeliveries(r.Context(), inbox.ID, webhookDeliveryLogSize)
		if err != nil {
			serverError(w, "listing webhook deliveries", err)
			return
		}
		writeJSON(w, deliveries)
	case "subscriptions":
		s.handleWebhookSubscriptions(w, r, inbox)
	case "redeliver":
		s.redeliverWebhook(w, r, inbox)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWebhookInboxCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		inboxes, err := s.stateDB.ListWebhookInboxes(r.Context())
		if err != nil {
			serverError(w, "listing webhook inboxes", err)
			return
		}
		views := []webhookInboxView{}
		for _, inbox := range inboxes {
			view, err := s.webhookInboxView(r, inbox)
			if err != nil {
				serverError(w, "reading webhook inbox", err)
				return
			}
			views = append(views, view)
		}
		writeJSON(w, views)
	case http.MethodPost:
		var req struct{ Name, EnrollmentToken, Secret, SecretHeader string }
		if !readAndUnmarshal(w, r, maxRequestBody, &req) {
			return
		}
		if req.Name = strings.TrimSpace(req.Name); req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		relayURL, ok := s.resolveWebhookRelay(w, r, &req.EnrollmentToken)
		if !ok {
			return
		}
		inbox, err := webhook.RegisterStandalone(r.Context(), s.stateDB, req.Name, relayURL, req.EnrollmentToken, req.Secret, req.SecretHeader, nil)
		if err != nil {
			serverError(w, "registering webhook inbox", err)
			return
		}
		if s.webhookCtx != nil {
			go (&webhook.Poller{Store: s.stateDB, Inbox: inbox, Routines: s.routineSvc}).Run(s.webhookCtx)
		}
		view, err := s.webhookInboxView(r, inbox)
		if err != nil {
			serverError(w, "reading webhook inbox", err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, view)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWebhookInboxItem(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	switch r.Method {
	case http.MethodGet:
		view, err := s.webhookInboxView(r, inbox)
		if err != nil {
			serverError(w, "reading webhook inbox", err)
			return
		}
		writeJSON(w, view)
	case http.MethodDelete:
		if err := (&share.RelayClient{BaseURL: inbox.RelayURL}).RevokeInbox(r.Context(), inbox.ID, inbox.ManagementToken); err != nil {
			serverError(w, "revoking webhook inbox", err)
			return
		}
		// The poller notices the missing row on its next tick and stops.
		if err := s.stateDB.DeleteWebhookInboxByID(r.Context(), inbox.ID); err != nil {
			serverError(w, "deleting webhook inbox", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		s.updateWebhookInbox(w, r, inbox)
	case http.MethodPut:
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
		// Serialize rotations so two resets can't leave the relay on one
		// recipient and state.db holding the other identity.
		s.webhookKeyMu.Lock()
		defer s.webhookKeyMu.Unlock()
		fresh, err := s.stateDB.GetWebhookInboxByID(r.Context(), inbox.ID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		inbox = fresh
		if req.Recipient == "" {
			id, err := age.GenerateX25519Identity()
			if err != nil {
				serverError(w, "generating webhook key", err)
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
		if err := s.stateDB.UpdateWebhookInboxKey(r.Context(), inbox.ID, inbox.Identity, version); err != nil {
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

// replaceWebhookSecret changes the secret on the relay, then records it. The
// lock spans both, so the stored secret is always the last one the relay took.
// ponytail: one lock for every inbox's key and secret changes; per-inbox locks if that contends.
func (s *Server) replaceWebhookSecret(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox, secret, header string) bool {
	s.webhookKeyMu.Lock()
	defer s.webhookKeyMu.Unlock()
	stored, err := (&share.RelayClient{BaseURL: inbox.RelayURL}).UpdateInboxSecret(r.Context(), inbox.ID, inbox.ManagementToken, secret, header)
	if err != nil {
		// An older relay has no secret endpoint (404/405); say so rather than 500.
		log.WithError(err).Warn("updating webhook secret")
		http.Error(w, "the relay could not update the secret; it may need upgrading", http.StatusBadGateway)
		return false
	}
	if err := s.stateDB.UpdateWebhookInboxSecret(r.Context(), inbox.ID, stored, secret); err != nil {
		serverError(w, "saving webhook secret", err)
		return false
	}
	return true
}

// updateWebhookInbox renames an inbox and, when secret is present, replaces
// or clears its shared secret on the relay. The ingestion URL is unchanged.
func (s *Server) updateWebhookInbox(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	var req struct {
		Name         *string `json:"name"`
		Secret       *string `json:"secret"`
		SecretHeader string  `json:"secretHeader"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	// Each field is written on its own, so a request snapshot can never put
	// back a value another request changed meanwhile.
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if err := s.stateDB.RenameWebhookInbox(r.Context(), inbox.ID, name); err != nil {
			serverError(w, "saving webhook inbox", err)
			return
		}
	}
	if req.Secret != nil && !s.replaceWebhookSecret(w, r, inbox, *req.Secret, strings.TrimSpace(req.SecretHeader)) {
		return
	}
	if fresh, err := s.stateDB.GetWebhookInboxByID(r.Context(), inbox.ID); err == nil {
		inbox = fresh
	}
	view, err := s.webhookInboxView(r, inbox)
	if err != nil {
		serverError(w, "reading webhook inbox", err)
		return
	}
	writeJSON(w, view)
}

// redeliverWebhook replays a logged delivery to the inbox's subscribers as if
// it had just arrived.
func (s *Server) redeliverWebhook(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		DeliveryID string `json:"deliveryId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	id, err := webhook.Redeliver(s.stateDB, s.routineSvc, inbox.ID, req.DeliveryID, time.Now())
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "delivery not found", http.StatusNotFound)
		return
	}
	if err != nil {
		serverError(w, "redelivering webhook", err)
		return
	}
	writeJSON(w, map[string]string{"deliveryId": id})
}

func (s *Server) handleWebhookSubscriptions(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	switch r.Method {
	case http.MethodGet:
		subs, err := s.stateDB.ListWebhookSubscriptions(r.Context(), inbox.ID)
		if err != nil {
			serverError(w, "listing webhook subscriptions", err)
			return
		}
		if subs == nil {
			subs = []state.WebhookSubscription{}
		}
		writeJSON(w, subs)
	case http.MethodPut:
		var sub state.WebhookSubscription
		if !readAndUnmarshal(w, r, maxRequestBody, &sub) {
			return
		}
		if !webhook.ValidPredicates(sub.HeaderPredicatesJSON) || !webhook.ValidPredicates(sub.JSONPredicatesJSON) {
			http.Error(w, "predicates must be a JSON object of predicate objects", http.StatusBadRequest)
			return
		}
		if _, err := s.routineSvc.Get(r.Context(), sub.RoutineID); err != nil {
			s.writeRoutineError(w, "getting routine", err)
			return
		}
		sub.InboxID = inbox.ID
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
