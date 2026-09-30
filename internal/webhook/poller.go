// Package webhook consumes encrypted relay inboxes on their owning ocman.
package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"filippo.io/age"
	"github.com/NoUseFreak/ocman/internal/relay"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	BatchSize    = 50
	PollInterval = 15 * time.Second
)

type Poller struct {
	Store    *state.DB
	Inbox    state.WebhookInbox
	HTTP     *http.Client
	Now      func() time.Time
	Routines RoutineDispatcher
}

// ErrInboxGone stops a poller whose inbox was revoked.
var ErrInboxGone = errors.New("webhook inbox no longer exists")

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		if errors.Is(p.Poll(ctx), ErrInboxGone) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func Register(ctx context.Context, store *state.DB, routineID, relayURL, enrollmentToken string, client *http.Client) (state.WebhookInbox, error) {
	return RegisterWithSecret(ctx, store, routineID, relayURL, enrollmentToken, "", "", client)
}

// RegisterWithSecret registers a legacy routine-owned inbox and subscribes
// that routine to every delivery.
func RegisterWithSecret(ctx context.Context, store *state.DB, routineID, relayURL, enrollmentToken, secret, secretHeader string, client *http.Client) (state.WebhookInbox, error) {
	inbox, err := register(ctx, store, "", routineID, relayURL, enrollmentToken, secret, secretHeader, client)
	if err != nil {
		return state.WebhookInbox{}, err
	}
	if err := store.SaveWebhookSubscription(ctx, state.WebhookSubscription{ID: "subscription-" + inbox.ID, InboxID: inbox.ID, RoutineID: routineID}); err != nil {
		return state.WebhookInbox{}, err
	}
	return inbox, nil
}

// RegisterStandalone registers a named inbox that no routine owns; routines
// subscribe to it separately.
func RegisterStandalone(ctx context.Context, store *state.DB, name, relayURL, enrollmentToken, secret, secretHeader string, client *http.Client) (state.WebhookInbox, error) {
	return register(ctx, store, name, "", relayURL, enrollmentToken, secret, secretHeader, client)
}

func register(ctx context.Context, store *state.DB, name, routineID, relayURL, enrollmentToken, secret, secretHeader string, client *http.Client) (state.WebhookInbox, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return state.WebhookInbox{}, fmt.Errorf("generating webhook identity: %w", err)
	}
	allocation, err := (share.RelayClient{BaseURL: relayURL, HTTP: client}).RegisterInboxWithSecret(ctx, identity.Recipient().String(), enrollmentToken, secret, secretHeader)
	if err != nil {
		return state.WebhookInbox{}, err
	}
	if routineID == "" {
		routineID = allocation.ID
	}
	inbox := state.WebhookInbox{ID: allocation.ID, Name: name, RoutineID: routineID, RelayURL: relayURL,
		ManagementToken: allocation.ManagementToken, FetchToken: allocation.FetchToken,
		AcknowledgmentToken: allocation.AcknowledgmentToken, Identity: identity.String(),
		IngestionURL: allocation.IngestionURL, KeyVersion: allocation.KeyVersion}
	if secret != "" {
		inbox.SecretHeader, inbox.Secret = allocation.SecretHeader, secret
	}
	if err := store.SaveWebhookInbox(ctx, inbox); err != nil {
		return state.WebhookInbox{}, err
	}
	return inbox, nil
}

// Poll traverses the complete relay cursor range. A bad delivery is recorded
// and skipped, so it cannot block newer deliveries behind it.
func (p *Poller) Poll(ctx context.Context) error {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	// Reload so a key rotation takes effect and a revoked inbox stops polling.
	switch inbox, err := p.Store.GetWebhookInboxByID(ctx, p.Inbox.ID); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrInboxGone
	case err == nil:
		p.Inbox = inbox
	}
	_ = p.Store.CleanupWebhookHistory(ctx, now().Add(-state.WebhookHistoryRetention).UnixMilli())
	identity, err := age.ParseX25519Identity(p.Inbox.Identity)
	if err != nil {
		return fmt.Errorf("parsing webhook identity: %w", err)
	}
	client := share.RelayClient{BaseURL: p.Inbox.RelayURL, HTTP: p.HTTP}
	cursor := ""
	for {
		page, err := client.ListInboxDeliveries(ctx, p.Inbox.ID, p.Inbox.FetchToken, cursor)
		if err != nil {
			return err
		}
		for _, delivery := range page.Deliveries {
			allowed, err := p.Store.WebhookDeliveryRetryAllowed(ctx, p.Inbox.ID, delivery.ID, now())
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			ciphertext, err := client.FetchInboxDelivery(ctx, p.Inbox.ID, delivery.ID, p.Inbox.FetchToken)
			if err == nil {
				var envelope relay.InboxEnvelope
				envelope, err = relay.DecryptInboxEnvelope(identity, p.Inbox.ID, delivery.ID, ciphertext)
				if err == nil {
					headers, _ := json.Marshal(envelope.Request.Header) // relay already dropped credential headers
					if _, dispatchErr := p.Store.AcceptWebhookDelivery(ctx, p.Inbox.ID, delivery.ID, envelope.Request.Method+" webhook", string(envelope.Body), string(headers), envelope.Request.ReceivedAt); dispatchErr != nil {
						err = dispatchErr
					} else if p.Routines != nil {
						err = Dispatch(p.Store, p.Routines, p.Inbox.ID, delivery.ID, envelope, now())
					}
					if err == nil {
						err = client.AcknowledgeInboxDelivery(ctx, p.Inbox.ID, delivery.ID, p.Inbox.AcknowledgmentToken)
					}
				}
			}
			if err != nil {
				if recordErr := p.Store.RecordWebhookDeliveryError(ctx, p.Inbox.ID, delivery.ID, err.Error(), now()); recordErr != nil {
					return recordErr
				}
			}
		}
		if page.Cursor == "" {
			return nil
		}
		cursor = page.Cursor
	}
}
