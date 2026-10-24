package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestRuntimeChildIdentityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		duplicate bool
		cancel    bool
		want      error
	}{
		{name: "missing", status: http.StatusOK, want: platforms.ErrNotFound},
		{name: "failed owner", status: http.StatusServiceUnavailable, want: platforms.ErrPlatformUnreachable},
		{name: "ambiguous owner", status: http.StatusOK, duplicate: true, want: platforms.ErrBusy},
		{name: "cancelled", status: http.StatusOK, cancel: true, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &claudeProjects{roots: map[string]*claudeProject{}}
			for _, root := range []string{"one", "two"} {
				a, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
					if tc.status != http.StatusOK {
						w.WriteHeader(tc.status)
						return
					}
					rows := []db.Session{}
					if tc.duplicate {
						rows = append(rows, db.Session{ID: "agent-child", ParentID: "root", Directory: "/repo"})
					}
					_ = json.NewEncoder(w).Encode(rows)
				})
				p.roots[root] = &claudeProject{watched: watched{adapter: a}}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			row, err := p.runtimeSession(ctx, "agent-child")
			if !errors.Is(err, tc.want) || row != nil || len(p.sessions) != 0 {
				t.Fatalf("unverified runtime identity: %+v %v", row, err)
			}
		})
	}
}
