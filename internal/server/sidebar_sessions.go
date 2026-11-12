package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// handleSidebarSessions deliberately bypasses summary fanout and applySessionState.
// Each owner returns filtered metadata, flags and observations in one SQL query.
func (s *Server) handleSidebarSessions(w http.ResponseWriter, r *http.Request) {
	q := platforms.SidebarQuery{Directory: r.URL.Query().Get("dir"), Since: sidebarSince(r), Limit: httpx.QueryInt(r, "limit", 500), Offset: httpx.QueryInt(r, "offset", 0), Origin: r.URL.Query().Get("origin"), OriginID: r.URL.Query().Get("originId")}
	if q.Limit < 0 || q.Offset < 0 {
		http.Error(w, "invalid sidebar page", http.StatusBadRequest)
		return
	}
	if q.Limit > 10000 || q.Offset > 1000000 {
		http.Error(w, "sidebar page too large", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("view") == "running-count" {
		count, err := s.runningSessionCount(r.Context(), q)
		if err != nil {
			httpx.ServerError(w, "counting running sessions", err)
			return
		}
		httpx.WriteJSON(w, struct {
			Count int `json:"count"`
		}{count})
		return
	}
	s.writeSessionSnapshot(w, r, func() (any, error) {
		q.Since = sidebarSince(r)
		return s.sidebarSessionRows(r.Context(), q, false)
	})
}

func (s *Server) sidebarSessionRows(ctx context.Context, q platforms.SidebarQuery, strict bool) ([]db.Session, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	adapters := s.registry.Platforms()
	var remoteFlags state.SidebarFlags
	if s.stateDB != nil {
		for _, a := range adapters {
			if !isLocalAdapter(a) {
				var err error
				remoteFlags, err = s.stateDB.SidebarFlags(ctx)
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}
	var local platforms.SidebarGroupReader
	sources := []platforms.SidebarSource{}
	for _, a := range adapters {
		if isLocalAdapter(a) {
			reader, ok := a.(platforms.SidebarGroupReader)
			if !ok {
				return nil, platforms.ErrUnsupported
			}
			source, err := reader.SidebarSource(ctx)
			if err != nil {
				return nil, err
			}
			if local == nil {
				local = reader
			}
			key, _ := s.registry.KeyFor(a)
			source.RoutingKey = string(key)
			sources = append(sources, source)
		}
	}
	results := make([][]db.Session, len(adapters))
	failures := make([]error, len(adapters))
	page := q
	page.Offset = 0
	if page.Limit > 0 {
		page.Limit += q.Offset
	}
	all := []db.Session{}
	// Start remote reads before the local query so owner latency overlaps.
	var wg sync.WaitGroup
	for i, a := range adapters {
		if isLocalAdapter(a) {
			continue
		}
		wg.Add(1)
		go func(i int, a platforms.Platform, ctx context.Context) {
			defer wg.Done()
			if !isLocalAdapter(a) {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
			}
			reader, ok := a.(platforms.SidebarReader)
			if !ok {
				failures[i] = platforms.ErrUnsupported
				return
			}
			remotePage := page
			statePlatform := s.sessionStatePlatform(string(a.ID()))
			remotePage.IncludeIDs = append([]string(nil), page.IncludeIDs...)
			for key := range remoteFlags.Pinned {
				if key.Platform == statePlatform {
					remotePage.IncludeIDs = append(remotePage.IncludeIDs, key.SessionID)
				}
			}
			for key, until := range remoteFlags.Snoozed {
				if until > 0 && key.Platform == statePlatform {
					remotePage.IncludeIDs = append(remotePage.IncludeIDs, key.SessionID)
				}
			}
			results[i], failures[i] = reader.SidebarSessions(ctx, remotePage)
		}(i, a, ctx)
	}
	if local != nil {
		rows, err := local.SidebarSessionsFor(ctx, sources, page)
		if err != nil {
			cancel()
			wg.Wait()
			return nil, err
		}
		for i := range rows {
			s.registry.RememberSessions(platforms.ID(rows[i].Platform), rows[i:i+1])
		}
		all = append(all, rows...)
	}
	wg.Wait()
	for i, rows := range results {
		if failures[i] != nil {
			continue
		}
		key, _ := s.registry.KeyFor(adapters[i])
		for j := range rows {
			rows[j].Platform = string(key)
		}
		s.registry.RememberSessions(key, rows)
		if !isLocalAdapter(adapters[i]) && s.stateDB != nil {
			applyRemoteSidebarFlags(rows, remoteFlags, s.sessionStatePlatform(string(key)))
		}
		if q.Since > 0 {
			kept := rows[:0]
			for _, row := range rows {
				if !row.Archived || row.TimeUpdated >= q.Since || row.Status == db.StatusBusy || row.Pinned || row.SnoozedUntil > 0 || row.PendingPermission || row.PendingQuestion {
					kept = append(kept, row)
				}
			}
			rows = kept
		}
		all = append(all, rows...)
	}
	if strict {
		if err := errors.Join(failures...); err != nil {
			return nil, err
		}
	}
	if err := s.overlayPRWatchTasks(ctx, all); err != nil {
		return nil, err
	}
	if err := s.holdArchivedSetup(ctx, all); err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].TimeUpdated == all[j].TimeUpdated {
			return all[i].ID < all[j].ID
		}
		return all[i].TimeUpdated > all[j].TimeUpdated
	})
	if q.Offset >= len(all) {
		all = []db.Session{}
	} else {
		all = all[q.Offset:]
		if q.Limit > 0 && len(all) > q.Limit {
			all = all[:q.Limit]
		}
	}
	if err := s.enrichSidebarCreators(ctx, all); err != nil {
		return nil, err
	}
	return all, nil
}

func (s *Server) supportsSidebar() bool {
	if s.registry == nil {
		return false
	}
	for _, a := range s.registry.Platforms() {
		if isLocalAdapter(a) {
			if _, ok := a.(platforms.SidebarGroupReader); !ok {
				return false
			}
		} else if _, ok := a.(platforms.SidebarReader); !ok {
			return false
		}
	}
	return true
}

func (s *Server) sidebarModules() []module.Module {
	var modules []module.Module
	if s.registry == nil {
		return modules
	}
	for _, adapter := range s.registry.Platforms() {
		if owner, ok := adapter.(interface{ SidebarModule() module.Module }); ok {
			modules = append(modules, owner.SidebarModule())
		}
	}
	return modules
}
