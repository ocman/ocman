package platforms

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

type metadataTestPlatform struct {
	*detailOnlyPlatform
	row *db.Session
	err error
}

func (p *metadataTestPlatform) SessionMetadata(context.Context, string) (*db.Session, error) {
	return p.row, p.err
}

func TestReadSessionMetadataFallbackOnlyForUnsupported(t *testing.T) {
	row := &db.Session{ID: "s", ParentID: "root"}
	for _, tc := range []struct {
		name     string
		metadata bool
		err      error
		fallback int
	}{
		{"metadata", true, nil, 0},
		{"older owner", true, ErrUnsupported, 1},
		{"no capability", false, nil, 1},
		{"offline", true, ErrRemoteOffline, 0},
		{"not found", true, ErrNotFound, 0},
		{"history unavailable", true, ErrHistoryUnavailable, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := &detailOnlyPlatform{detail: &SessionDetail{Session: row}}
			var p Platform = legacy
			if tc.metadata {
				p = &metadataTestPlatform{detailOnlyPlatform: legacy, row: row, err: tc.err}
			}
			got, err := ReadSessionMetadata(t.Context(), p, "s")
			wantErr := tc.err
			if errors.Is(wantErr, ErrUnsupported) {
				wantErr = nil
			}
			if !errors.Is(err, wantErr) || legacy.calls != tc.fallback || err == nil && got != row {
				t.Fatalf("row=%+v err=%v fallback calls=%d", got, err, legacy.calls)
			}
		})
	}
}
