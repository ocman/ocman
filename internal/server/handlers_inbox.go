package server

import (
	"context"
	"net/http"
	"sort"
	"sync"

	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

const inboxFanoutLimit = 8

type inboxItemView struct {
	ID         string                 `json:"id"`
	Title      string                 `json:"title"`
	Body       string                 `json:"body"`
	CreatedAt  int64                  `json:"createdAt"`
	ReadAt     int64                  `json:"readAt,omitempty"`
	ArchivedAt int64                  `json:"archivedAt,omitempty"`
	RemoteID   string                 `json:"remoteId"`
	Category   string                 `json:"category"`
	Permission *state.InboxPermission `json:"permission,omitempty"`
}

type inboxRef struct {
	ID       string `json:"id"`
	RemoteID string `json:"remoteId"`
}
type inboxMutationRequest struct {
	ID       string     `json:"id"`
	RemoteID string     `json:"remoteId"`
	IDs      []string   `json:"ids"`
	Items    []inboxRef `json:"items"`
}

func (s *Server) inboxSources() []string {
	if s.inboxSourcesFn != nil {
		return s.inboxSourcesFn()
	}
	if s.remotes != nil {
		return s.remotes.InboxSources()
	}
	if s.stateDB != nil {
		return []string{"local"}
	}
	return nil
}

func (s *Server) inboxItems(ctx context.Context, source string, archived bool) ([]state.InboxItem, error) {
	if s.inboxItemsFn != nil && !archived {
		return s.inboxItemsFn(ctx, source)
	}
	if s.remotes != nil {
		if archived {
			return s.remotes.ArchivedInboxItems(ctx, source)
		}
		return s.remotes.InboxItems(ctx, source)
	}
	if source != "local" || s.stateDB == nil {
		return nil, remote.ErrRemoteOffline
	}
	if archived {
		return s.stateDB.ListArchivedInboxItems(ctx)
	}
	return s.stateDB.ListInboxItems(ctx)
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleInboxList(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/api/inbox/read", "/api/inbox/open", "/api/inbox/unread":
		s.handleInboxRead(w, r)
	case "/api/inbox/archive":
		s.handleInboxArchive(w, r, false)
	case "/api/inbox/archive-all-read":
		s.handleInboxArchive(w, r, true)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleInboxList(w http.ResponseWriter, r *http.Request) {
	sources := s.inboxSources()
	type result struct {
		source string
		items  []state.InboxItem
		err    error
	}
	results := make([]result, len(sources))
	sem := make(chan struct{}, inboxFanoutLimit)
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func(i int, source string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx := r.Context()
			if source != "local" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, remoteFanoutTimeout)
				defer cancel()
			}
			items, err := s.inboxItems(ctx, source, r.URL.Query().Get("archived") == "true")
			results[i] = result{source, items, err}
		}(i, source)
	}
	wg.Wait()
	items := []inboxItemView{}
	unread := 0
	for _, result := range results {
		if result.err != nil {
			if result.source == "local" {
				serverError(w, "listing Inbox items", result.err)
				return
			}
			continue
		}
		for _, item := range result.items {
			if item.Category == "" {
				item.Category = state.InboxGeneral
			}
			if item.Permission != nil && result.source != "local" {
				permission := *item.Permission
				permission.Platform = remote.CompoundPlatformID(result.source, permission.Platform)
				item.Permission = &permission
			}
			items = append(items, inboxItemView{item.ID, item.Title, item.Body, item.CreatedAt, item.ReadAt, item.ArchivedAt, result.source, item.Category, item.Permission})
			if item.ReadAt == 0 && item.ArchivedAt == 0 {
				unread++
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		if items[i].ID != items[j].ID {
			return items[i].ID > items[j].ID
		}
		return items[i].RemoteID > items[j].RemoteID
	})
	writeJSON(w, map[string]any{"items": items, "unreadTotal": unread})
}

func (s *Server) handleInboxRead(w http.ResponseWriter, r *http.Request) {
	var req inboxMutationRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.ID == "" || req.RemoteID == "" {
		http.Error(w, "id and remoteId are required", http.StatusBadRequest)
		return
	}
	if !s.inboxOwnerAvailable(req.RemoteID) {
		writeInboxOwnerError(w, req.RemoteID)
		return
	}
	var err error
	if r.URL.Path == "/api/inbox/unread" {
		err = s.inboxMarkUnread(r.Context(), req.RemoteID, req.ID)
	} else {
		err = s.inboxMarkRead(r.Context(), req.RemoteID, req.ID)
	}
	if err != nil {
		serverError(w, "updating Inbox item read state", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInboxArchive(w http.ResponseWriter, r *http.Request, allRead bool) {
	var req inboxMutationRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if allRead {
		if req.RemoteID == "" {
			http.Error(w, "remoteId is required", http.StatusBadRequest)
			return
		}
		if !s.inboxOwnerAvailable(req.RemoteID) {
			writeInboxOwnerError(w, req.RemoteID)
			return
		}
		if err := s.inboxArchive(r.Context(), req.RemoteID, nil, true); err != nil {
			serverError(w, "archiving read Inbox items", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	refs := append([]inboxRef{}, req.Items...)
	if len(refs) == 0 && req.ID != "" {
		refs = []inboxRef{{req.ID, req.RemoteID}}
	}
	if len(refs) == 0 {
		for _, id := range req.IDs {
			refs = append(refs, inboxRef{id, req.RemoteID})
		}
	}
	if len(refs) == 0 {
		http.Error(w, "items are required", http.StatusBadRequest)
		return
	}
	groups := map[string][]string{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.ID == "" || ref.RemoteID == "" {
			http.Error(w, "each item requires id and remoteId", http.StatusBadRequest)
			return
		}
		key := ref.RemoteID + "\x00" + ref.ID
		if !seen[key] {
			seen[key] = true
			groups[ref.RemoteID] = append(groups[ref.RemoteID], ref.ID)
		}
	}
	for source := range groups {
		if !s.inboxOwnerAvailable(source) {
			writeInboxOwnerError(w, source)
			return
		}
	}
	for source, ids := range groups {
		if err := s.inboxArchive(r.Context(), source, ids, false); err != nil {
			serverError(w, "archiving Inbox items", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) inboxOwnerAvailable(source string) bool {
	for _, candidate := range s.inboxSources() {
		if candidate == source {
			return true
		}
	}
	return false
}
func (s *Server) inboxMarkRead(ctx context.Context, source, id string) error {
	if s.remotes != nil {
		return s.remotes.MarkInboxItemRead(ctx, source, id)
	}
	return s.stateDB.MarkInboxItemRead(ctx, id)
}
func (s *Server) inboxMarkUnread(ctx context.Context, source, id string) error {
	if s.remotes != nil {
		return s.remotes.MarkInboxItemUnread(ctx, source, id)
	}
	return s.stateDB.MarkInboxItemUnread(ctx, id)
}
func (s *Server) inboxArchive(ctx context.Context, source string, ids []string, allRead bool) error {
	if s.remotes != nil {
		return s.remotes.ArchiveInboxItems(ctx, source, ids, allRead)
	}
	if allRead {
		return s.stateDB.ArchiveAllReadInboxItems(ctx)
	}
	return s.stateDB.ArchiveInboxItems(ctx, ids)
}
func writeInboxOwnerError(w http.ResponseWriter, source string) {
	writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "remote_not_connected", "remoteId": source, "message": "remote " + source + " is not connected"}})
}
