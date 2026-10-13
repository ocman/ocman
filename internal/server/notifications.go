package server

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/notifications"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (s *Server) notificationsModule() *notifications.Module {
	s.notificationsModOnce.Do(func() {
		s.notificationsMod = notifications.New(notifications.Deps{
			Store: s.stateDB, Snapshot: s.pushSnapshot,
			Guards: module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		})
	})
	return s.notificationsMod
}

// Reuse the notification projection: it already folds child prompts into
// visible parents, suppresses autoapproved permissions and routes remote owners.
func (s *Server) pushSnapshot(ctx context.Context) ([]notifications.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	all, err := s.fanOutSessionsResult(ctx, "", time.Now().Add(-24*time.Hour).UnixMilli(), nil)
	if err != nil {
		return nil, err
	}
	entries, err := s.notificationEntriesForSessions(ctx, sortAndLimitSessions(all, 500), true)
	if err != nil {
		return nil, err
	}
	var out []notifications.Candidate
	for _, entry := range entries {
		target := "/session/" + url.PathEscape(entry.ID) + "?platform=" + url.QueryEscape(entry.platform)
		tag := "ocman:" + entry.platform + ":" + entry.ID
		add := func(key, kind string) {
			title, body := "ocman — session finished", "A coding-agent session has finished running."
			if kind == "prompt" {
				title, body = "ocman — input required", "A session is waiting on your response."
			}
			out = append(out, notifications.Candidate{Key: tag + ":" + key, Notification: notifications.Notification{
				Title: title, Body: body, URL: target, Tag: tag + ":" + kind, Kind: kind,
			}})
		}
		if entry.PendingPermission || entry.PendingQuestion {
			for _, refs := range []*[]notifyPrompt{entry.Permissions, entry.Questions} {
				if refs != nil {
					for _, ref := range *refs {
						add(ref.SessionID+":"+ref.RequestID, "prompt")
					}
				}
			}
			continue // A prompt takes precedence over terminal status.
		}
		if entry.Seen || entry.SuppressTerminal || (entry.Status != db.StatusWaiting && entry.Status != db.StatusError && entry.Status != db.StatusDone) {
			continue
		}
		// Message identity survives seen/title changes and repeated idle edges.
		if s.registry == nil {
			continue
		}
		adapter, ok := s.registry.Get(platforms.ID(entry.platform))
		if !ok {
			continue
		}
		reader, ok := adapter.(platforms.LifecycleReader)
		if !ok {
			continue
		}
		life, err := reader.SessionLifecycle(ctx, entry.ID)
		if err != nil {
			return nil, fmt.Errorf("reading notification lifecycle for %s/%s: %w", entry.platform, entry.ID, err)
		}
		if life == nil {
			return nil, fmt.Errorf("missing notification lifecycle for %s/%s", entry.platform, entry.ID)
		}
		if life.LatestMessageRole != "assistant" || life.LatestMessageID == "" {
			continue
		}
		if life.Status != db.StatusWaiting && life.Status != db.StatusError && life.Status != db.StatusDone {
			continue // A stale snapshot must not notify a turn that resumed.
		}
		add("completed:"+life.LatestMessageID, "completed")
	}
	return out, nil
}
