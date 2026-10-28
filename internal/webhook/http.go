package webhook

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/share"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

// deliveryLogSize bounds the debugging log; bodies can be large.
const deliveryLogSize = 10

// InboxView is an inbox without its relay credentials or private key.
// Secret is the user's own shared secret, shown back so it can be checked.
type InboxView struct {
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

func (m *Module) view(r *http.Request, inbox state.WebhookInbox) (InboxView, error) {
	counts, err := m.store.WebhookDispatchCounts(r.Context(), inbox.ID)
	if err != nil {
		return InboxView{}, err
	}
	subs, err := m.store.ListWebhookSubscriptions(r.Context(), inbox.ID)
	if err != nil {
		return InboxView{}, err
	}
	if subs == nil {
		subs = []state.WebhookSubscription{}
	}
	return InboxView{inbox.ID, inbox.Name, inbox.RelayURL, inbox.IngestionURL, inbox.KeyVersion, inbox.CreatedAt, inbox.SecretHeader, inbox.Secret, counts, subs}, nil
}

// routes registers /api/webhook-inboxes[/{id}[/deliveries|/subscriptions|/redeliver]].
// Inboxes only capture deliveries; routines subscribe to them with filters.
func (m *Module) routes(handle func(string, http.HandlerFunc)) {
	for pattern, h := range map[string]http.HandlerFunc{
		"GET /api/webhook-inboxes":                       m.listInboxes,
		"POST /api/webhook-inboxes":                      m.createInbox,
		"GET /api/webhook-inboxes/{id}":                  m.withInbox(m.getInbox),
		"DELETE /api/webhook-inboxes/{id}":               m.withInbox(m.revokeInbox),
		"PATCH /api/webhook-inboxes/{id}":                m.withInbox(m.updateInbox),
		"PUT /api/webhook-inboxes/{id}":                  m.withInbox(m.rotateInbox),
		"GET /api/webhook-inboxes/{id}/deliveries":       m.withInbox(m.listDeliveries),
		"GET /api/webhook-inboxes/{id}/subscriptions":    m.withInbox(m.listSubscriptions),
		"PUT /api/webhook-inboxes/{id}/subscriptions":    m.withInbox(m.saveSubscription),
		"DELETE /api/webhook-inboxes/{id}/subscriptions": m.withInbox(m.deleteSubscription),
		"POST /api/webhook-inboxes/{id}/redeliver":       m.withInbox(m.redeliver),
	} {
		handle(pattern, h)
		handle(pattern+"/{$}", h) // a trailing slash is accepted too
	}
}

func (m *Module) withInbox(h func(http.ResponseWriter, *http.Request, state.WebhookInbox)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inbox, err := m.store.GetWebhookInboxByID(r.Context(), r.PathValue("id"))
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			httpx.ServerError(w, "getting webhook inbox", err)
			return
		}
		h(w, r, inbox)
	}
}

func (m *Module) listDeliveries(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	deliveries, err := m.store.ListWebhookDeliveries(r.Context(), inbox.ID, deliveryLogSize)
	if err != nil {
		httpx.ServerError(w, "listing webhook deliveries", err)
		return
	}
	httpx.WriteJSON(w, deliveries)
}

func (m *Module) listInboxes(w http.ResponseWriter, r *http.Request) {
	inboxes, err := m.store.ListWebhookInboxes(r.Context())
	if err != nil {
		httpx.ServerError(w, "listing webhook inboxes", err)
		return
	}
	views := []InboxView{}
	for _, inbox := range inboxes {
		view, err := m.view(r, inbox)
		if err != nil {
			httpx.ServerError(w, "reading webhook inbox", err)
			return
		}
		views = append(views, view)
	}
	httpx.WriteJSON(w, views)
}

func (m *Module) createInbox(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, EnrollmentToken, Secret, SecretHeader string }
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.Name = strings.TrimSpace(req.Name); req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	relayURL, ok := m.resolveRelay(w, r, &req.EnrollmentToken)
	if !ok {
		return
	}
	inbox, err := RegisterStandalone(r.Context(), m.store, req.Name, relayURL, req.EnrollmentToken, req.Secret, req.SecretHeader, nil)
	if err != nil {
		httpx.ServerError(w, "registering webhook inbox", err)
		return
	}
	m.startPoller(inbox)
	view, err := m.view(r, inbox)
	if err != nil {
		httpx.ServerError(w, "reading webhook inbox", err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, view)
}

func (m *Module) getInbox(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	view, err := m.view(r, inbox)
	if err != nil {
		httpx.ServerError(w, "reading webhook inbox", err)
		return
	}
	httpx.WriteJSON(w, view)
}

func (m *Module) revokeInbox(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	if err := (&share.RelayClient{BaseURL: inbox.RelayURL}).RevokeInbox(r.Context(), inbox.ID, inbox.ManagementToken); err != nil {
		httpx.ServerError(w, "revoking webhook inbox", err)
		return
	}
	// The poller notices the missing row on its next tick and stops.
	if err := m.store.DeleteWebhookInboxByID(r.Context(), inbox.ID); err != nil {
		httpx.ServerError(w, "deleting webhook inbox", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) rotateInbox(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	var req struct {
		Recipient string `json:"recipient"`
		Reset     bool   `json:"reset"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.Reset {
		req.Recipient = ""
	}
	// Serialize rotations so two resets can't leave the relay on one
	// recipient and state.db holding the other identity.
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	fresh, err := m.store.GetWebhookInboxByID(r.Context(), inbox.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inbox = fresh
	if req.Recipient == "" {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			httpx.ServerError(w, "generating webhook key", err)
			return
		}
		req.Recipient = id.Recipient().String()
		inbox.Identity = id.String()
	}
	version, err := (&share.RelayClient{BaseURL: inbox.RelayURL}).RotateInbox(r.Context(), inbox.ID, inbox.ManagementToken, req.Recipient)
	if err != nil {
		httpx.ServerError(w, "rotating webhook inbox", err)
		return
	}
	inbox.KeyVersion = version
	if err := m.store.UpdateWebhookInboxKey(r.Context(), inbox.ID, inbox.Identity, version); err != nil {
		httpx.ServerError(w, "saving webhook key", err)
		return
	}
	httpx.WriteJSON(w, struct {
		KeyVersion int  `json:"keyVersion"`
		Reset      bool `json:"reset"`
	}{version, req.Reset})
}

// replaceSecret changes the secret on the relay, then records it. The
// lock spans both, so the stored secret is always the last one the relay took.
// ponytail: one lock for every inbox's key and secret changes; per-inbox locks if that contends.
func (m *Module) replaceSecret(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox, secret, header string) bool {
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	stored, err := (&share.RelayClient{BaseURL: inbox.RelayURL}).UpdateInboxSecret(r.Context(), inbox.ID, inbox.ManagementToken, secret, header)
	if err != nil {
		// An older relay has no secret endpoint (404/405); say so rather than 500.
		log.WithError(err).Warn("updating webhook secret")
		http.Error(w, "the relay could not update the secret; it may need upgrading", http.StatusBadGateway)
		return false
	}
	if err := m.store.UpdateWebhookInboxSecret(r.Context(), inbox.ID, stored, secret); err != nil {
		httpx.ServerError(w, "saving webhook secret", err)
		return false
	}
	return true
}

// updateInbox renames an inbox and, when secret is present, replaces
// or clears its shared secret on the relay. The ingestion URL is unchanged.
func (m *Module) updateInbox(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	var req struct {
		Name         *string `json:"name"`
		Secret       *string `json:"secret"`
		SecretHeader string  `json:"secretHeader"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
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
		if err := m.store.RenameWebhookInbox(r.Context(), inbox.ID, name); err != nil {
			httpx.ServerError(w, "saving webhook inbox", err)
			return
		}
	}
	if req.Secret != nil && !m.replaceSecret(w, r, inbox, *req.Secret, strings.TrimSpace(req.SecretHeader)) {
		return
	}
	if fresh, err := m.store.GetWebhookInboxByID(r.Context(), inbox.ID); err == nil {
		inbox = fresh
	}
	view, err := m.view(r, inbox)
	if err != nil {
		httpx.ServerError(w, "reading webhook inbox", err)
		return
	}
	httpx.WriteJSON(w, view)
}

// redeliver replays a logged delivery to the inbox's subscribers as if
// it had just arrived.
func (m *Module) redeliver(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	var req struct {
		DeliveryID string `json:"deliveryId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	id, err := Redeliver(r.Context(), m.store, m.routines, inbox.ID, req.DeliveryID, time.Now())
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "delivery not found", http.StatusNotFound)
		return
	}
	if err != nil {
		httpx.ServerError(w, "redelivering webhook", err)
		return
	}
	httpx.WriteJSON(w, map[string]string{"deliveryId": id})
}

func (m *Module) listSubscriptions(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	subs, err := m.store.ListWebhookSubscriptions(r.Context(), inbox.ID)
	if err != nil {
		httpx.ServerError(w, "listing webhook subscriptions", err)
		return
	}
	if subs == nil {
		subs = []state.WebhookSubscription{}
	}
	httpx.WriteJSON(w, subs)
}

func (m *Module) saveSubscription(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	var sub state.WebhookSubscription
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &sub) {
		return
	}
	if !ValidPredicates(sub.HeaderPredicatesJSON) || !ValidPredicates(sub.JSONPredicatesJSON) {
		http.Error(w, "predicates must be a JSON object of predicate objects", http.StatusBadRequest)
		return
	}
	routine, err := m.routines.Get(r.Context(), sub.RoutineID)
	if errors.Is(err, state.ErrRoutineNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		httpx.ServerError(w, "getting routine", err)
		return
	}
	if !IsLocal(routine.RemoteID) {
		http.Error(w, "a webhook can only trigger a routine on this machine", http.StatusBadRequest)
		return
	}
	sub.InboxID = inbox.ID
	if sub.ID == "" {
		sub.ID = "subscription-" + inbox.ID + "-" + sub.RoutineID
	}
	if err := m.store.SaveWebhookSubscription(r.Context(), sub); err != nil {
		httpx.ServerError(w, "saving webhook subscription", err)
		return
	}
	httpx.WriteJSON(w, sub)
}

func (m *Module) deleteSubscription(w http.ResponseWriter, r *http.Request, inbox state.WebhookInbox) {
	var sub struct {
		RoutineID string `json:"routineId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &sub) {
		return
	}
	if err := m.store.DeleteWebhookSubscription(r.Context(), inbox.ID, sub.RoutineID); err != nil {
		httpx.ServerError(w, "deleting webhook subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
