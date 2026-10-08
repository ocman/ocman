package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type summaryPlatform struct {
	fakePlatform
	summaryCalls, detailCalls int
	row                       db.Session
}

func (p *summaryPlatform) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	p.summaryCalls++
	if id != p.row.ID {
		return nil, platforms.ErrNotFound
	}
	return &p.row, nil
}

func (p *summaryPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.detailCalls++
	return &platforms.SessionDetail{Session: &p.row}, nil
}

func TestPinnedSessionsUseSummaryWithoutTranscript(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	for _, owner := range []string{"opencode", "r-box:opencode"} {
		p := &summaryPlatform{fakePlatform: fakePlatform{id: owner}, row: mkSession(owner, "old", owner, 1)}
		reg.Register(p)
		for _, id := range []string{"old", "deleted"} {
			if err := s.stateDB.PinSession(t.Context(), owner, id); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			if p.detailCalls != 0 || p.summaryCalls != 2 {
				t.Errorf("%s: detail=%d summary=%d", owner, p.detailCalls, p.summaryCalls)
			}
		})
	}
	rr := httptest.NewRecorder()
	s.handleSessions(rr, httptest.NewRequest(http.MethodGet, "/api/sessions?since=10000", nil))
	var rows []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &rows)
	if rr.Code != 200 || len(rows) != 2 {
		t.Fatalf("response = %d %s", rr.Code, rr.Body)
	}
	for _, row := range rows {
		if row.ID != "old" || !row.Pinned || row.Title != row.Platform {
			t.Errorf("row = %+v", row)
		}
	}
}
