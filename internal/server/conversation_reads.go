package server

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/conversationread"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ConversationReadContext is used only for explicit transcript browsing, also
// on owner-side Session RPCs. It never decorates mutations or event streams.
func (s *Server) ConversationReadContext(ctx context.Context) context.Context {
	reader := s.conversationReader
	if reader == nil {
		reader = &conversationread.Service{}
	}
	return platforms.WithConversationReader(ctx, reader)
}

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
