// Package webhook consumes encrypted relay inboxes on their owning ocman.
package webhook

import (
	"context"
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

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	_ = p.Poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.Poll(ctx)
		}
	}
}

func Register(ctx context.Context, store *state.DB, routineID, relayURL, enrollmentToken string, client *http.Client) (state.WebhookInbox, error) {
	return RegisterWithSecret(ctx, store, routineID, relayURL, enrollmentToken, "", "", client)
}

func RegisterWithSecret(ctx context.Context, store *state.DB, routineID, relayURL, enrollmentToken, secret, secretHeader string, client *http.Client) (state.WebhookInbox, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return state.WebhookInbox{}, fmt.Errorf("generating webhook identity: %w", err)
	}
	allocation, err := (share.RelayClient{BaseURL: relayURL, HTTP: client}).RegisterInboxWithSecret(ctx, identity.Recipient().String(), enrollmentToken, secret, secretHeader)
	if err != nil {
		return state.WebhookInbox{}, err
	}
	inbox := state.WebhookInbox{ID: allocation.ID, RoutineID: routineID, RelayURL: relayURL,
		ManagementToken: allocation.ManagementToken, FetchToken: allocation.FetchToken,
		AcknowledgmentToken: allocation.AcknowledgmentToken, Identity: identity.String(), KeyVersion: allocation.KeyVersion}
	if err := store.SaveWebhookInbox(ctx, inbox); err != nil {
		return state.WebhookInbox{}, err
	}
	if err := store.SaveWebhookSubscription(ctx, state.WebhookSubscription{ID: "subscription-" + allocation.ID, InboxID: allocation.ID, RoutineID: routineID}); err != nil {
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
					if accepted, dispatchErr := p.Store.AcceptWebhookDelivery(ctx, p.Inbox.ID, delivery.ID, envelope.Request.Method+" webhook", string(envelope.Body), envelope.Request.ReceivedAt); dispatchErr != nil {
						err = dispatchErr
					} else if accepted && p.Routines != nil {
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
