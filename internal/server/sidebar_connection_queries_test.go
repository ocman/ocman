package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type countedSidebarConnection struct {
	*compactTestPlatform
	queries   *atomic.Int64
	coreQuery func(context.Context, string) bool
}

func (p *countedSidebarConnection) Owns(ctx context.Context, id string) bool {
	p.queries.Add(1)
	return p.coreQuery(ctx, id)
}

func TestColdSidebarDoesNotQueryCatalogueForEveryRoutingKey(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	var queries atomic.Int64
	rows := make([]db.Session, 500)
	for i := range rows {
		rows[i] = db.Session{ID: fmt.Sprintf("ocm_row_%d", i), Platform: "cnx_work", TimeUpdated: int64(i + 1), Status: db.StatusDone}
	}
	for _, key := range []string{"cnx_personal", "cnx_work"} {
		p := &countedSidebarConnection{compactTestPlatform: &compactTestPlatform{fakePlatform: &fakePlatform{PlatformID: "claudecode"}, rows: rows}, queries: &queries,
			coreQuery: func(ctx context.Context, id string) bool {
				var found string
				return s.stateDB.SQL().QueryRowContext(ctx, `SELECT id FROM session_catalog WHERE id=?`, id).Scan(&found) == nil
			},
		}
		if err := reg.RegisterConnection(platforms.ID(key), p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.sidebarSessionRows(t.Context(), platforms.SidebarQuery{Limit: 500}, false)
	if err != nil || len(got) != 500 {
		t.Fatalf("page: %d %v", len(got), err)
	}
	if got := queries.Load(); got != 0 {
		t.Fatalf("cold sidebar added %d catalogue SELECTs for 500 rows", got)
	}
	for _, row := range got {
		if row.Platform != "cnx_work" {
			t.Fatal("group projection lost routing identity", row.Platform)
		}
	}
}
