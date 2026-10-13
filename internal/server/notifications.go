package server

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
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

// PermissionNotificationDeferred is wired into the owner's authenticated RPC
// server, so the hub never guesses the remote judge's state.
func (s *Server) PermissionNotificationDeferred(session, request string) bool {
	return s.aaSvc().DeferPermissionNotification(session, request)
}

// Reuse the notification projection: it already folds child prompts into
// visible parents, suppresses autoapproved permissions and routes remote owners.
func (s *Server) pushSnapshot(ctx context.Context) ([]notifications.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	all, err := s.fanOutSessionsResult(ctx, "", time.Now().Add(-7*24*time.Hour).UnixMilli(), nil)
	if err != nil {
		return nil, err
	}
	entries, err := s.notificationEntriesForSessions(ctx, sortAndLimitSessions(all, 500), true, true)
	if err != nil {
		return nil, err
	}
	var out []notifications.Candidate
	for _, entry := range entries {
		target := "/session/" + url.PathEscape(entry.ID) + "?platform=" + url.QueryEscape(entry.platform) + "&notification=1"
		tag := "ocman:" + entry.platform + ":" + entry.ID
		add := func(key, kind, action string) {
			title, body := notificationText(entry.Title, entry.Directory, action)
			out = append(out, notifications.Candidate{Key: tag + ":" + key, Notification: notifications.Notification{
				Title: title, Body: body, URL: target, Tag: tag + ":" + kind, Kind: kind,
			}})
		}
		if entry.PendingPermission || entry.PendingQuestion {
			for index, refs := range []*[]notifyPrompt{entry.Permissions, entry.Questions} {
				if refs != nil {
					for _, ref := range *refs {
						action := "Permission needed"
						if index == 1 {
							action = "Answer needed"
						}
						add(ref.SessionID+":"+ref.RequestID, "prompt", action)
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
		action := "Finished"
		if life.Status == db.StatusError {
			action = "Failed"
		}
		add("completed:"+life.LatestMessageID, "completed", action)
	}
	attention := make(map[[2]string]bool)
	for _, entry := range entries {
		attention[[2]string{entry.platform,entry.ID}] = true
	}
	for i := range out {
		out[i].Notification.BadgeCount = len(attention)
	}
	return out, nil
}

func notificationText(title, directory, action string) (string, string) {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = "Untitled session"
	}
	if chars := []rune(title); len(chars) > 160 {
		title = string(chars[:160]) + "…"
	}
	project := path.Base(strings.ReplaceAll(directory, `\`, "/"))
	if project == "." || project == "/" {
		return title, action
	}
	if chars := []rune(project); len(chars) > 80 {
		project = string(chars[:80]) + "…"
	}
	return title, action + " · " + project
}
