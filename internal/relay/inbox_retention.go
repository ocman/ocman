package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ocman/ocman/internal/share"
)

// finishInboxPoll renews only authenticated, successful list requests. Empty
// polls count; incoming webhooks and management reads do not. Serialize the
// metadata update with rotations, revocation and expiry to avoid lost updates.
func (s *Server) finishInboxPoll(w http.ResponseWriter, r *http.Request, id string, page inboxDeliveryPage) {
	s.mutations.Lock()
	defer s.mutations.Unlock()
	m, ok := s.authoriseInbox(w, r, id, func(m inboxMeta) string { return m.FetchHash })
	if !ok {
		return
	}
	m.LastPolledAt = s.cfg.Now().UnixMilli()
	if err := putInboxMeta(r.Context(), s.cfg.Store, id, m); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// Retention timestamps live outside deliveries/ so ciphertext format, quota
// accounting, and cursor pagination stay unchanged.
func inboxDeliveryTimeKey(inboxID, deliveryID string) string {
	return "inboxes/" + inboxID + "/delivery-times/" + deliveryID
}

func (s *Server) putInboxDelivery(ctx context.Context, inboxID, deliveryID string, ciphertext []byte) error {
	key := inboxDeliveryTimeKey(inboxID, deliveryID)
	data := []byte(strconv.FormatInt(s.cfg.Now().UnixMilli(), 10))
	if err := s.cfg.Store.Put(ctx, key, data); err != nil {
		return err
	}
	if err := s.cfg.Store.Put(ctx, inboxDeliveryKey(inboxID, deliveryID), ciphertext); err != nil {
		return errors.Join(err, s.cfg.Store.DeletePrefix(ctx, key))
	}
	return nil
}

func (s *Server) deleteInboxDelivery(ctx context.Context, inboxID, deliveryID string) error {
	if err := s.cfg.Store.DeletePrefix(ctx, inboxDeliveryKey(inboxID, deliveryID)); err != nil {
		return err
	}
	return s.cfg.Store.DeletePrefix(ctx, inboxDeliveryTimeKey(inboxID, deliveryID))
}

func (s *Server) sweepInbox(ctx context.Context, id string, now time.Time) error {
	s.mutations.Lock()
	defer s.mutations.Unlock()
	data, err := s.cfg.Store.Get(ctx, inboxMetaKey(id))
	if errors.Is(err, share.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("relay: reading inbox metadata: %w", err)
	}
	var m inboxMeta
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	lastPolled := m.LastPolledAt
	if lastPolled == 0 {
		// The old format cannot tell us when its owner last polled. Give
		// existing inboxes one inactivity window rather than deleting an
		// actively used inbox just because it was created before the upgrade.
		lastPolled = now.UnixMilli()
		m.LastPolledAt = lastPolled
		if err := putInboxMeta(ctx, s.cfg.Store, id, m); err != nil {
			return fmt.Errorf("relay: initializing inbox activity: %w", err)
		}
	}
	if lastPolled > 0 && !now.Before(time.UnixMilli(lastPolled).Add(s.cfg.InboxTTL)) {
		if err := s.cfg.Store.DeletePrefix(ctx, "inboxes/"+id); err != nil {
			return fmt.Errorf("relay: expiring inbox: %w", err)
		}
		return nil
	}
	objects, err := s.cfg.Store.List(ctx, inboxDeliveryPrefix(id))
	if err != nil {
		return fmt.Errorf("relay: listing inbox deliveries: %w", err)
	}
	for _, object := range objects {
		deliveryID, ok := deliveryIDFromKey(object.Key)
		if !ok {
			continue
		}
		if err := s.expireInboxDelivery(ctx, id, deliveryID, now); err != nil {
			return fmt.Errorf("relay: expiring delivery: %w", err)
		}
	}
	return nil
}

func (s *Server) expireInboxDelivery(ctx context.Context, inboxID, deliveryID string, now time.Time) error {
	key := inboxDeliveryTimeKey(inboxID, deliveryID)
	data, err := s.cfg.Store.Get(ctx, key)
	if errors.Is(err, share.ErrNotFound) {
		// Older relays stored no plaintext timestamp. Preserve pending
		// ciphertext and start a full retention window on the first sweep.
		data = []byte(strconv.FormatInt(now.UnixMilli(), 10))
		return s.cfg.Store.Put(ctx, key, data)
	}
	if err != nil {
		return err
	}
	var createdAt int64
	if err := json.Unmarshal(data, &createdAt); err != nil {
		return err
	}
	if createdAt <= 0 {
		return errors.New("invalid inbox delivery timestamp")
	}
	if !now.Before(time.UnixMilli(createdAt).Add(s.cfg.InboxDeliveryTTL)) {
		return s.deleteInboxDelivery(ctx, inboxID, deliveryID)
	}
	return nil
}
