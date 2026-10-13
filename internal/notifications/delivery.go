package notifications

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	log "github.com/sirupsen/logrus"
)

func (m *Module) deliver(ctx context.Context) error {
	due, err := m.store.due(ctx)
	if err != nil || len(due) == 0 {
		return err
	}
	private, public, err := m.store.keys(ctx)
	if err != nil {
		return err
	}
	for _, d := range due {
		active, err := m.store.active(ctx, d.endpoint)
		if err != nil {
			return err
		}
		if !active {
			continue
		}
		var sub webpush.Subscription
		if err := json.Unmarshal([]byte(d.subscription), &sub); err != nil {
			return err
		}
		if !validSubscription(sub) {
			return m.store.remove(ctx, d.endpoint)
		}
		response, sendErr := webpush.SendNotificationWithContext(ctx, []byte(d.payload), &sub, &webpush.Options{
			HTTPClient: m.client, Subscriber: "https://github.com/NoUseFreak/ocman",
			VAPIDPrivateKey: private, VAPIDPublicKey: public, TTL: 300, Urgency: webpush.UrgencyHigh,
		})
		success := false
		delay := time.Duration(1<<d.attempts) * 10 * time.Second
		if response != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusGone || response.StatusCode == http.StatusNotFound {
				if err := m.store.remove(ctx, d.endpoint); err != nil {
					return err
				}
				continue
			}
			success = response.StatusCode >= 200 && response.StatusCode < 300
			if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 3600 {
				delay = time.Duration(seconds) * time.Second
			}
			if !success {
				log.WithField("status", response.StatusCode).Warn("Web Push service rejected notification")
			}
		}
		if sendErr != nil && ctx.Err() == nil {
			// Endpoint URLs contain credentials; do not log the transport error.
			log.Warn("Web Push delivery failed; retrying")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.store.finish(ctx, d, success, delay); err != nil {
			return err
		}
	}
	return nil
}
