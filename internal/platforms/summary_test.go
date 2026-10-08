package platforms

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

type detailOnlyPlatform struct {
	Platform
	detail *SessionDetail
	err    error
	calls  int
}

func (p *detailOnlyPlatform) Session(context.Context, string, int, int) (*SessionDetail, error) {
	p.calls++
	return p.detail, p.err
}

type summaryTestPlatform struct {
	*detailOnlyPlatform
	row *db.Session
	err error
}

func (p *summaryTestPlatform) SessionSummary(context.Context, string) (*db.Session, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.row, nil
}

func TestReadSessionSummary(t *testing.T) {
	row := &db.Session{ID: "s"}
	for _, tc := range []struct {
		name                  string
		summary               bool
		summaryErr, detailErr error
		detail                *SessionDetail
		calls                 int
		want                  *db.Session
		err                   error
	}{
		{name: "summary", summary: true, want: row},
		{name: "missing summary", summary: true, summaryErr: ErrNotFound, err: ErrNotFound},
		{name: "older remote", summary: true, summaryErr: ErrUnsupported, detail: &SessionDetail{Session: row}, calls: 1, want: row},
		{name: "legacy", detail: &SessionDetail{Session: row}, calls: 1, want: row},
		{name: "nil detail", calls: 1},
		{name: "detail error", detailErr: ErrNotFound, calls: 1, err: ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := &detailOnlyPlatform{detail: tc.detail, err: tc.detailErr}
			var p Platform = legacy
			if tc.summary {
				p = &summaryTestPlatform{detailOnlyPlatform: legacy, row: row, err: tc.summaryErr}
			}
			got, err := ReadSessionSummary(t.Context(), p, "s")
			if got != tc.want || !errors.Is(err, tc.err) || legacy.calls != tc.calls {
				t.Fatalf("got=%+v err=%v calls=%d", got, err, legacy.calls)
			}
		})
	}
}
