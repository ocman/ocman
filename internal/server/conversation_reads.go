package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (s *Server) conversationReadHeader(w http.ResponseWriter, adapter platforms.Platform, detail *platforms.SessionDetail) {
	source := detail.ConversationSource
	if source == "" {
		source = "platform-live"
	}
	if owner, _ := platforms.SplitPlatformID(string(adapter.ID())); owner != "" {
		source = "remote"
	}
	w.Header().Set("X-Ocman-Conversation-Source", source)
}
