package app

import (
	"fmt"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

// historyDBEnv overrides the statev2.db path; the -statev2-db flag wins.
const historyDBEnv = "OCMAN_STATEV2_DB"

// openConversationHistory opens the mandatory owner-local conversation store.
func openConversationHistory(statePath, override string, getenv func(string) string) (*statev2.DB, error) {
	path := override
	if path == "" {
		path = getenv(historyDBEnv)
	}
	if path == "" {
		path = statev2.Path(statePath)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	store, err := statev2.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening conversation history: %w", err)
	}
	log.WithField("path", path).Info("conversation history storage opened")
	return store, nil
}
