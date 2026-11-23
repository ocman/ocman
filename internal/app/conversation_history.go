package app

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/ocman/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

// The legacy flag and environment variable remain compatibility aliases.
const historyDBEnv = "OCMAN_CONVERSATIONS_DB"

func conversationHistoryFlag(flags *flag.FlagSet) *string {
	path := flags.String("conversations-db", "", "conversation database path (default: $OCMAN_CONVERSATIONS_DB, else state_conv.db beside state.db)")
	flags.StringVar(path, "statev2-db", "", "deprecated alias for -conversations-db")
	return path
}

func conversationHistoryPath(statePath, override string, getenv func(string) string) string {
	if override != "" {
		return override
	}
	if path := getenv(historyDBEnv); path != "" {
		return path
	}
	if path := getenv("OCMAN_STATEV2_DB"); path != "" {
		return path
	}
	return statev2.Path(statePath)
}

// openConversationHistory opens the mandatory owner-local conversation store.
func openConversationHistory(statePath, override string, getenv func(string) string) (*statev2.DB, error) {
	path, err := filepath.Abs(conversationHistoryPath(statePath, override, getenv))
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
