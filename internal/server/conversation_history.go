package server

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/analytics"
	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

// WithConversationHistory installs owner-local ingestion before modules
// are built. The caller owns the database lifetime and closes it only after
// Start returns, when the module's independent workers have all been joined.
// A nil store leaves historical readers unavailable, never platform-backed.
func (s *Server) WithConversationHistory(store *statev2.DB, ownerID string) *Server {
	if store == nil {
		return s
	}
	s.historyOwner = ownerID
	s.historyCommits = newHistoryCommits()
	store.OnCommitted(s.historyCommits.observe)
	deps := conversationingest.Deps{Store: store, OwnerID: ownerID,
		OnError: func(instanceID, sessionID string, err error) {
			log.WithError(err).WithFields(log.Fields{"instance_id": instanceID, "session_id": sessionID}).Warn("conversation history repair failed; will retry")
		}}
	if s.stateDB != nil {
		deps.Tasks = s.taskManager()
		catalogue, _ := sessioncatalog.New(s.stateDB)
		s.sessions.WithSessionCatalogue(catalogue, ownerID)
	}
	for _, adapter := range s.registry.Platforms() {
		if remoteID, _ := platforms.SplitPlatformID(string(adapter.ID())); remoteID != "" {
			continue
		}
		if reader, ok := adapter.(platforms.HistoryReader); ok {
			deps.Sources = append(deps.Sources, conversationingest.Source{AdapterID: string(adapter.ID()), Label: adapter.DisplayName(), Reader: reader})
		}
	}
	s.historyIngest = conversationingest.NewModule(deps)
	for _, adapter := range s.registry.Platforms() {
		if binder, ok := adapter.(platforms.StoredHistoryBinder); ok {
			binder.BindStoredHistory(platforms.StoredHistoryHooks{
				Notify: func(id string) { s.historyIngest.Notify(string(adapter.ID()), id) },
				Await: func(ctx context.Context, id string) bool {
					return s.historyIngest.AwaitStored(ctx, string(adapter.ID()), id)
				},
				Refresh: func(ctx context.Context, id string) error {
					return s.historyIngest.RefreshStored(ctx, string(adapter.ID()), id)
				},
			})
		}
	}
	s.historyAnalytics = &analytics.HistoryAnalytics{Store: store, CheckedAt: s.historyIngest.LastCheckedAt, Instances: func() ([]string, bool) {
		ids, complete := s.historyIngest.AnalyticsInstances()
		// Only local adapters contribute to owner-local history. Remote
		// discovery must not invalidate an otherwise completed local import.
		for _, adapter := range s.registry.Platforms() {
			if remoteID, _ := platforms.SplitPlatformID(string(adapter.ID())); remoteID != "" {
				continue
			}
			found := false
			for _, source := range deps.Sources {
				found = found || source.AdapterID == string(adapter.ID())
			}
			if !found {
				return ids, false
			}
		}
		return ids, complete
	}}
	return s
}

func (s *Server) observeHistoryChange(event string, data []byte) {
	if s.historyIngest == nil {
		return
	}
	switch event {
	case "ocman.session.changed", "ocman.session.activity", "ocman.session.idle", "ocman.session.status":
	default:
		return
	}
	var change struct {
		Platform  string `json:"platform"`
		SessionID string `json:"sessionID"`
	}
	if json.Unmarshal(data, &change) != nil {
		return
	}
	s.historyIngest.Observe(change.Platform, change.SessionID)
}
