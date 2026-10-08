package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// notifyEntry is the bounded payload shared by all notification consumers.
// Prompt identities let the browser reconcile a resolution that happened
// while the snapshot was in flight, including prompts on native children.
type notifyEntry struct {
	platform          string
	terminalEligible  bool
	ID                string           `json:"id"`
	Status            db.SessionStatus `json:"status"`
	Seen              bool             `json:"seen"`
	PendingPermission bool             `json:"pendingPermission,omitempty"`
	PendingQuestion   bool             `json:"pendingQuestion,omitempty"`
	Title             string           `json:"title,omitempty"`
	Directory         string           `json:"directory,omitempty"`
	Permissions       *[]notifyPrompt  `json:"permissions,omitempty"`
	Questions         *[]notifyPrompt  `json:"questions,omitempty"`
	SuppressTerminal  bool             `json:"suppressTerminal,omitempty"`
}

type notifyPrompt struct {
	Platform  string `json:"platform"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
}

// Only pending prompts and unseen waiting/error/interrupted sessions can
// contribute to notification state; keep the response small.
func (s *Server) handleSessionsNotify(w http.ResponseWriter, r *http.Request) {
	since := parseInt64Param(r, "since", 0)
	limit := parseIntParam(r, "limit", 500)
	ctx := r.Context()
	all := sortAndLimitSessions(s.fanOutSessions(ctx, "", since, nil), limit)
	if err := s.applyNotifySessionState(ctx, all); err != nil {
		serverError(w, "fetching session state for notify", err)
		return
	}
	out := make([]notifyEntry, 0, len(all))
	for i := range all {
		se := &all[i]
		deferredPermission := se.PendingPermission && s.deferPermissionNotification(ctx, se)
		pendingPermission := se.PendingPermission && !deferredPermission
		hasPrompt := pendingPermission || se.PendingQuestion
		isUnseenTerminal := (se.Status == db.StatusError || se.Status == db.StatusInterrupted || (se.Status == db.StatusWaiting && !deferredPermission)) && !se.Seen
		if !hasPrompt && !isUnseenTerminal {
			continue
		}
		entry := notifyEntry{
			platform: se.Platform, terminalEligible: isUnseenTerminal,
			ID: se.ID, Status: se.Status, Seen: se.Seen,
			PendingPermission: pendingPermission, PendingQuestion: se.PendingQuestion,
			Title: se.Title, Directory: se.Directory,
			SuppressTerminal: deferredPermission && se.Status == db.StatusWaiting,
		}
		out = append(out, entry)
	}
	s.enrichNotifyPrompts(ctx, all, out)
	eligible := out[:0]
	for _, entry := range out {
		if entry.PendingPermission || entry.PendingQuestion || entry.terminalEligible {
			eligible = append(eligible, entry)
		}
	}
	writeJSON(w, eligible)
}

// Every owner's identity reads share one bounded budget, in parallel, so a
// stalled remote cannot hold healthy owners behind a serial question scan.
func (s *Server) enrichNotifyPrompts(ctx context.Context, sessions []db.Session, entries []notifyEntry) {
	ctx, cancel := context.WithTimeout(ctx, remoteFanoutTimeout)
	defer cancel()
	// Entry ordering matches the filtered session ordering.
	var wg sync.WaitGroup
	index := 0
	for i := range sessions {
		if index >= len(entries) {
			break
		}
		if entries[index].ID != sessions[i].ID || entries[index].platform != sessions[i].Platform {
			continue
		}
		entry, row := &entries[index], &sessions[i]
		index++
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.notifyPromptIdentities(ctx, row, entry)
		}()
	}
	wg.Wait()
}

func (s *Server) notifyPromptIdentities(ctx context.Context, session *db.Session, entry *notifyEntry) {
	if s.registry == nil || (!entry.PendingPermission && !entry.PendingQuestion) {
		return
	}
	adapter, ok := s.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return
	}
	var permissions, questions []platforms.LivePrompt
	var permissionErr, questionErr error
	if cached, ok := adapter.(interface {
		NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error)
	}); ok {
		permissions, questions, permissionErr = cached.NotificationPrompts(ctx, session.ID)
		questionErr = permissionErr
	} else if isRemotePlatformID(session.Platform) {
		if entry.PendingPermission {
			permissions, permissionErr = adapter.ListPermissions(ctx, session.ID)
		}
		if entry.PendingQuestion {
			questions, questionErr = adapter.ListQuestions(ctx, session.ID)
		}
	} else {
		return
	}
	refs := func(prompts []platforms.LivePrompt) []notifyPrompt {
		out := []notifyPrompt{}
		for _, prompt := range prompts {
			id, _ := prompt["id"].(string)
			sid, _ := prompt["sessionID"].(string)
			if id != "" && sid != "" {
				out = append(out, notifyPrompt{Platform: session.Platform, SessionID: sid, RequestID: id})
			}
		}
		return out
	}
	if entry.PendingPermission && permissionErr == nil {
		identities := refs(permissions)
		entry.Permissions = &identities
		entry.PendingPermission = len(identities) > 0
	}
	if entry.PendingQuestion && questionErr == nil {
		identities := refs(questions)
		entry.Questions = &identities
		entry.PendingQuestion = len(identities) > 0
	}
}

func (s *Server) deferPermissionNotification(ctx context.Context, session *db.Session) bool {
	if isRemotePlatformID(session.Platform) || s.registry == nil {
		return false
	}
	adapter, ok := s.registry.Get(platforms.ID(session.Platform))
	if !ok || !adapter.Capabilities().AutoApprove {
		return false
	}
	enabled := s.autoApproveDefault
	if s.stateDB != nil {
		value, exists, err := s.stateDB.GetAutoApprove(ctx, session.Platform, session.ID)
		if err != nil {
			return false
		}
		if exists {
			enabled = value
		}
	}
	if !enabled {
		return false
	}
	prompts, err := adapter.ListPermissions(ctx, session.ID)
	if err != nil || len(prompts) == 0 {
		return false
	}
	for _, prompt := range prompts {
		permissionID, _ := prompt["id"].(string)
		promptSessionID, _ := prompt["sessionID"].(string)
		if permissionID == "" || promptSessionID == "" || !s.aaSvc().DeferPermissionNotification(promptSessionID, permissionID) {
			return false
		}
	}
	return true
}
