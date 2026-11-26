package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	log "github.com/sirupsen/logrus"
)

type prSidebarCache struct {
	rows    map[string][]platforms.PullRequestSidebarLink // nil until synced; immutable after publication
	refresh chan struct{}
}

func (p *remotePlatform) PullRequestSidebar() ([]platforms.PullRequestSidebarLink, bool) {
	p.mu.RLock()
	snapshot := p.prSidebar.rows
	p.mu.RUnlock()
	var rows []platforms.PullRequestSidebarLink
	for _, links := range snapshot {
		rows = append(rows, links...)
	}
	return rows, snapshot != nil
}

// Sync independently of both the browser and the stream of session changes.
// One bulk read per platform avoids making an owner call per open sidebar.
func syncPRSidebar(ctx context.Context, adapters []*remotePlatform) {
	var workers sync.WaitGroup
	for _, p := range adapters {
		workers.Go(func() { module.RunWithRecover("remote-pr-sidebar", func() { p.runPRSidebarSync(ctx) }) })
	}
	workers.Wait()
}

func (p *remotePlatform) runPRSidebarSync(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil && p.syncPRSidebar(ctx) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.prSidebar.refresh:
		}
	}
}

// Return false only when the peer definitively does not support the snapshot.
func (p *remotePlatform) syncPRSidebar(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := p.PullRequestOperation(ctx, platforms.PullRequestRequest{Operation: "sidebar-links"})
	if errors.Is(err, platforms.ErrUnsupported) || response.Status == http.StatusBadRequest {
		return false
	}
	if err != nil || response.Status != http.StatusOK {
		return true // keep last successful snapshot on transient failures
	}
	var links []platforms.PullRequestSidebarLink
	if err := json.Unmarshal(response.Body, &links); err != nil {
		log.WithError(err).WithField("platform", p.ID()).Warn("remote: decoding PR sidebar snapshot")
		return true
	}
	next := make(map[string][]platforms.PullRequestSidebarLink)
	for _, l := range links {
		if l.SessionID != "" && (!p.CanonicalSessionIDs() || identity.IsSessionID(l.SessionID)) {
			next[l.SessionID] = append(next[l.SessionID], l)
		}
	}
	p.mu.Lock()
	previous := p.prSidebar.rows
	p.prSidebar.rows = next
	p.mu.Unlock()
	for id, rows := range next {
		if !reflect.DeepEqual(rows, previous[id]) {
			p.publishSessionChange(id)
		}
	}
	for id := range previous {
		if _, found := next[id]; !found {
			p.publishSessionChange(id)
		}
	}
	return true
}
