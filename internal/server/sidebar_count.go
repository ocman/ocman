package server

import (
	"context"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func (s *Server) runningSessionCount(ctx context.Context, q platforms.SidebarQuery) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	adapters := s.registry.Platforms()
	var local platforms.RunningSessionGroupCounter
	var sources []platforms.SidebarSource
	var flags state.SidebarFlags
	for _, a := range adapters {
		if !isLocalAdapter(a) {
			continue
		}
		reader, ok := a.(platforms.SidebarGroupReader)
		if !ok {
			return 0, platforms.ErrUnsupported
		}
		source, err := reader.SidebarSource(ctx)
		if err != nil {
			return 0, err
		}
		if local == nil {
			local, ok = a.(platforms.RunningSessionGroupCounter)
			if !ok {
				return 0, platforms.ErrUnsupported
			}
		}
		key, _ := s.registry.KeyFor(a)
		source.RoutingKey = string(key)
		sources = append(sources, source)
	}
	for _, a := range adapters {
		if !isLocalAdapter(a) && s.stateDB != nil {
			var err error
			flags, err = s.stateDB.SidebarFlags(ctx)
			if err != nil {
				return 0, err
			}
			break
		}
	}
	counts := make([]int, len(adapters))
	var wg sync.WaitGroup
	for i, a := range adapters {
		if isLocalAdapter(a) {
			continue
		}
		reader, ok := a.(platforms.RunningSessionCounter)
		if !ok {
			continue // Same partial-owner policy as compact sidebar reads.
		}
		page := q
		page.IncludeIDs = append([]string(nil), q.IncludeIDs...)
		for key := range flags.Pinned {
			if key.Platform == s.sessionStatePlatform(string(a.ID())) {
				page.IncludeIDs = append(page.IncludeIDs, key.SessionID)
			}
		}
		for key, until := range flags.Snoozed {
			if until > 0 && key.Platform == s.sessionStatePlatform(string(a.ID())) {
				page.IncludeIDs = append(page.IncludeIDs, key.SessionID)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if count, err := reader.RunningSessionCount(ctx, page); err == nil {
				counts[i] = count
			}
		}()
	}
	count := 0
	if local != nil {
		var err error
		count, err = local.RunningSessionCountFor(ctx, sources, q)
		if err != nil {
			cancel()
			wg.Wait()
			return 0, err
		}
	}
	wg.Wait()
	for _, n := range counts {
		count += n
	}
	return count, nil
}
