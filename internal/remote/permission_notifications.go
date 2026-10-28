package remote

import (
	"context"
	"maps"

	"github.com/ocman/ocman/internal/platforms"
)

func (s *Server) UsePermissionNotificationDeferrer(fn func(string, string) bool) *Server {
	s.permissionNotificationDeferred = fn
	return s
}

func (s *Server) permissionNotifications(ctx context.Context, p platforms.Platform, session string) ([]platforms.LivePrompt, error) {
	prompts, err := p.ListPermissions(ctx, session)
	if err != nil || s.permissionNotificationDeferred == nil {
		return prompts, err
	}
	out := make([]platforms.LivePrompt, len(prompts))
	for i, prompt := range prompts {
		out[i] = make(platforms.LivePrompt, len(prompt)+1)
		maps.Copy(out[i], prompt)
		sid, _ := prompt["sessionID"].(string)
		id, _ := prompt["id"].(string)
		out[i][platforms.NotificationDeferredField] = s.permissionNotificationDeferred(sid, id)
	}
	return out, nil
}
