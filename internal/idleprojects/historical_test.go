package idleprojects

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestMissingHistoricalCheckoutDoesNotDisableUnrelatedCleanup(t *testing.T) {
	for _, owner := range []string{"", "/unrelated"} {
		m, p, r := setup(t)
		p.sessions = append(p.sessions, db.Session{ID: "old", Directory: "/deleted/checkout", Status: db.StatusDone})
		r.missing = map[string]bool{"/deleted/checkout": true}
		if owner != "" {
			r.historical = map[string]string{"old": owner}
		}
		_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
		if err != nil || !idle {
			t.Fatalf("retained unrelated transcript blocked cleanup: idle=%v err=%v", idle, err)
		}
	}
}

func TestMissingHistoricalMemberPreservesArchiveIntentAndUncertainOwnership(t *testing.T) {
	m, p, r := setup(t)
	p.sessions = append(p.sessions, db.Session{ID: "old", Directory: "/deleted/checkout", Status: db.StatusDone})
	r.missing = map[string]bool{"/deleted/checkout": true}
	r.historical = map[string]string{"old": "/repo"}
	_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err != nil || idle {
		t.Fatal("forgot unarchived historical member", err)
	}
	r.historyError = errors.New("ownership is unreadable")
	_, _, idle, err = m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err == nil || idle {
		t.Fatal("uncertain ownership allowed cleanup")
	}
}
