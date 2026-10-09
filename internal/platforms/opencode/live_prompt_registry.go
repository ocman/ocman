package opencode

import (
	"sync"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type livePromptEntry struct {
	directory string
	prompt    platforms.LivePrompt
}

type livePromptRegistry struct {
	mu           sync.RWMutex
	entries      map[string]livePromptEntry
	version      map[string]uint64
	changed      map[string]uint64
	changeScope  map[string]string
	applied      map[string]uint64
	scopePort    map[string]string
	portGen      map[string]uint64
	haltStore    *state.DB
	haltReceipts map[string]int64
	lastHalts    map[string]int64
}

func newLivePromptRegistry() *livePromptRegistry {
	return &livePromptRegistry{
		entries:     make(map[string]livePromptEntry),
		version:     make(map[string]uint64),
		changed:     make(map[string]uint64),
		changeScope: make(map[string]string),
		applied:     make(map[string]uint64),
		scopePort:   make(map[string]string),
		portGen:     make(map[string]uint64),
	}
}

func promptKey(kind, sessionID, requestID string) string {
	return kind + "\x00" + sessionID + "\x00" + requestID
}

func promptScope(directory, kind string) string { return directory + "\x00" + kind }

func promptString(prompt platforms.LivePrompt, key string) string {
	value, _ := prompt[key].(string)
	return value
}

func clonePrompt(prompt platforms.LivePrompt) platforms.LivePrompt {
	out := make(platforms.LivePrompt, len(prompt))
	for key, value := range prompt {
		out[key] = value
	}
	return out
}
