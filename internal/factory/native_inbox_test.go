package factory

import (
	"context"
	"strings"
	"testing"
)

type nativeInboxStoreFake struct {
	nativeStoreFake
	category, title, body, platform, sessionID string
}

func (s *nativeInboxStoreFake) NotifySessionInbox(_ context.Context, title, body, category, platform, sessionID string) error {
	s.title, s.body, s.category, s.platform, s.sessionID = title, body, category, platform, sessionID
	return nil
}

func TestFactoryDeliveryInboxCategory(t *testing.T) {
	store := &nativeInboxStoreFake{}
	NewNative(store).notifyEpicDelivered(t.Context(), "epic-1", "Ready for review", "https://forge.example/pulls/1", "opencode", "ses-delivery")
	if store.category != "factory" || !strings.Contains(store.body, "Ready for review") || store.title == "" || store.platform != "opencode" || store.sessionID != "ses-delivery" {
		t.Fatalf("notification: %+v", store)
	}
}
