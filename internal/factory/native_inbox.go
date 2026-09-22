package factory

import (
	"context"

	"github.com/sirupsen/logrus"
)

// notifyEpicDelivered drops an Inbox item when an Epic's delivery attempt
// lands a PR. Soft-fail: the handoff already succeeded, so a missing inbox
// only loses the notification.
func (s *NativeService) notifyEpicDelivered(ctx context.Context, epicID, summary, prURL, platform, sessionID string) {
	inbox, ok := s.store.(interface {
		NotifySessionInbox(context.Context, string, string, string, string, string) error
	})
	if !ok {
		return
	}
	title := "Factory epic delivered"
	if epic, err := s.store.GetFactoryEpic(ctx, epicID); err == nil && epic.Goal != "" {
		title = "Factory delivered: " + epic.Goal
	}
	body := summary + "\n\nPR: " + prURL + "\nEpic: " + epicID
	if err := inbox.NotifySessionInbox(ctx, title, body, "factory", platform, sessionID); err != nil {
		logrus.WithError(err).WithField("epic", epicID).Warn("factory: inbox notification failed")
	}
}
