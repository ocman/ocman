package server

import (
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/analytics"
	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/conversationread"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/historyparity"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
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
	deps := conversationingest.Deps{Store: store, OwnerID: ownerID,
		OnError: func(instanceID, sessionID string, err error) {
			log.WithError(err).WithFields(log.Fields{"instance_id": instanceID, "session_id": sessionID}).Warn("conversation history repair failed; will retry")
		}}
	if s.stateDB != nil {
		deps.Tasks = s.taskManager()
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
	s.conversationReader = &conversationread.Service{Store: store, OwnerID: ownerID, Notify: s.historyIngest.Notify, Await: s.historyIngest.Await}
	s.historyAnalytics = &analytics.HistoryAnalytics{Store: store, CheckedAt: s.historyIngest.LastCheckedAt, Instances: func() ([]string, bool) {
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
				return nil, false
			}
		}
		return s.historyIngest.ReadyInstances()
	}}
	// Parity compares only the OpenCode history instance, so its source must
	// exclude the other platforms' rows the analytics mirror merges in.
	if opencode, ok := s.openCodeAnalytics(); ok {
		parity := historyparity.Deps{Source: opencode, Store: store, OwnerID: ownerID}
		if s.stateDB != nil {
			parity.Tasks = s.taskManager()
		}
		s.historyParity = historyparity.NewModule(parity)
	}
	return s
}

func (s *Server) openCodeAnalytics() (*db.DB, bool) {
	if s.db == nil {
		return nil, false
	}
	return s.db.OpenCodeOnly()
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
	s.historyIngest.Notify(change.Platform, change.SessionID)
}
