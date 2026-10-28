package inbox

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// ItemView is one Inbox item as listed, tagged with its owning machine.
type ItemView struct {
	ID         string                 `json:"id"`
	Title      string                 `json:"title"`
	Body       string                 `json:"body"`
	CreatedAt  int64                  `json:"createdAt"`
	ReadAt     int64                  `json:"readAt,omitempty"`
	ArchivedAt int64                  `json:"archivedAt,omitempty"`
	RemoteID   string                 `json:"remoteId"`
	Category   string                 `json:"category"`
	Permission *state.InboxPermission `json:"permission,omitempty"`
	Session    *state.InboxSession    `json:"session,omitempty"`
	Pinned     bool                   `json:"pinned"`
	PinnedAt   int64                  `json:"pinnedAt,omitempty"`
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

func (m *Module) remotes() Remotes {
	if m.Remotes == nil {
		return nil
	}
	return m.Remotes()
}

func (m *Module) inboxSources() []string {
	if r := m.remotes(); r != nil {
		return r.InboxSources()
	}
	return []string{"local"}
}

func (m *Module) inboxItems(ctx context.Context, source string, archived bool) ([]state.InboxItem, error) {
	if r := m.remotes(); r != nil {
		if archived {
			return r.ArchivedInboxItems(ctx, source)
		}
		return r.InboxItems(ctx, source)
	}
	if source != "local" {
		return nil, platforms.ErrRemoteOffline
	}
	if archived {
		return m.Store.ListArchivedInboxItems(ctx)
	}
	return m.Store.ListInboxItems(ctx)
}

func (m *Module) handleInboxList(w http.ResponseWriter, r *http.Request) {
	sources := m.inboxSources()
	type result struct {
		source string
		items  []state.InboxItem
		err    error
	}
	results := make([]result, len(sources))
	sem := make(chan struct{}, fanoutLimit)
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
				ctx, cancel = context.WithTimeout(ctx, remoteTimeout)
				defer cancel()
			}
			items, err := m.inboxItems(ctx, source, r.URL.Query().Get("archived") == "true")
			if err == nil {
				items = m.inboxSessionContext(ctx, source, items)
			}
			results[i] = result{source, items, err}
		}(i, source)
	}
	wg.Wait()
	items := []ItemView{}
	pinned, err := m.Store.PinnedInboxItems(r.Context())
	if err != nil {
		httpx.ServerError(w, "listing pinned Inbox items", err)
		return
	}
	unread := 0
	for _, result := range results {
		if result.err != nil {
			if result.source == "local" {
				httpx.ServerError(w, "listing Inbox items", result.err)
				return
			}
			continue
		}
		for _, item := range result.items {
			if result.source != "local" && item.Category == state.InboxFactory && strings.HasPrefix(item.ID, "factory-action-") {
				// Factory is owner-local; a remote's relative link would open the hub's graph.
				body, _, _ := strings.Cut(item.Body, "\n\n[Open Factory actions](/factory/epics/")
				item.Body = body + "\n\nOpen Factory on the owning machine to resolve this action."
			}
			if item.Category == "" {
				item.Category = state.InboxGeneral
			}
			if item.Permission != nil && result.source != "local" {
				permission := *item.Permission
				permission.Platform = platforms.CompoundPlatformID(result.source, permission.Platform)
				item.Permission = &permission
			}
			pinnedAt := pinned[state.InboxKey{RemoteID: result.source, ItemID: item.ID}]
			items = append(items, ItemView{item.ID, item.Title, item.Body, item.CreatedAt, item.ReadAt, item.ArchivedAt, result.source, item.Category, item.Permission, item.Session, pinnedAt > 0, pinnedAt})
			if item.ReadAt == 0 && item.ArchivedAt == 0 {
				unread++
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		if items[i].PinnedAt != items[j].PinnedAt {
			return items[i].PinnedAt > items[j].PinnedAt
		}
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		if items[i].ID != items[j].ID {
			return items[i].ID > items[j].ID
		}
		return items[i].RemoteID > items[j].RemoteID
	})
	httpx.WriteJSON(w, map[string]any{"items": items, "unreadTotal": unread})
}

func (m *Module) handleInboxPin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		RemoteID string `json:"remoteId"`
		Pinned   bool   `json:"pinned"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.ID == "" || req.RemoteID == "" {
		http.Error(w, "id and remoteId are required", http.StatusBadRequest)
		return
	}
	if !m.inboxOwnerAvailable(req.RemoteID) {
		writeInboxOwnerError(w, req.RemoteID)
		return
	}
	var err error
	if req.Pinned {
		err = m.Store.PinInboxItem(r.Context(), req.RemoteID, req.ID)
	} else {
		err = m.Store.UnpinInboxItem(r.Context(), req.RemoteID, req.ID)
	}
	if err != nil {
		httpx.ServerError(w, "updating pinned Inbox item state", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleInboxRead(w http.ResponseWriter, r *http.Request) {
	var req inboxMutationRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.ID == "" || req.RemoteID == "" {
		http.Error(w, "id and remoteId are required", http.StatusBadRequest)
		return
	}
	if !m.inboxOwnerAvailable(req.RemoteID) {
		writeInboxOwnerError(w, req.RemoteID)
		return
	}
	var err error
	if r.URL.Path == "/api/inbox/unread" {
		err = m.inboxMarkUnread(r.Context(), req.RemoteID, req.ID)
	} else {
		err = m.inboxMarkRead(r.Context(), req.RemoteID, req.ID)
	}
	if err != nil {
		httpx.ServerError(w, "updating Inbox item read state", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleInboxArchive(w http.ResponseWriter, r *http.Request) {
	var req inboxMutationRequest
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
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
		if !m.inboxOwnerAvailable(source) {
			writeInboxOwnerError(w, source)
			return
		}
	}
	for source, ids := range groups {
		if err := m.inboxArchive(r.Context(), source, ids); err != nil {
			httpx.ServerError(w, "archiving Inbox items", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) inboxOwnerAvailable(source string) bool {
	for _, candidate := range m.inboxSources() {
		if candidate == source {
			return true
		}
	}
	return false
}
func (m *Module) inboxMarkRead(ctx context.Context, source, id string) error {
	if r := m.remotes(); r != nil {
		return r.MarkInboxItemRead(ctx, source, id)
	}
	return m.Store.MarkInboxItemRead(ctx, id)
}
func (m *Module) inboxMarkUnread(ctx context.Context, source, id string) error {
	if r := m.remotes(); r != nil {
		return r.MarkInboxItemUnread(ctx, source, id)
	}
	return m.Store.MarkInboxItemUnread(ctx, id)
}
func (m *Module) inboxArchive(ctx context.Context, source string, ids []string) error {
	if r := m.remotes(); r != nil {
		return r.ArchiveInboxItems(ctx, source, ids)
	}
	return m.Store.ArchiveInboxItems(ctx, ids)
}
func writeInboxOwnerError(w http.ResponseWriter, source string) {
	httpx.WriteJSONStatus(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "remote_not_connected", "remoteId": source, "message": "remote " + source + " is not connected"}})
}
