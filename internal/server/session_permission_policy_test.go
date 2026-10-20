package server

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestPermissionPolicyParentFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		detail     *platforms.SessionDetail
		err        error
		wantParent string
		wantErr    bool
	}{
		{name: "nested subagent", detail: &platforms.SessionDetail{Session: &db.Session{ParentID: "parent"}}, wantParent: "parent"},
		{name: "top-level session", detail: &platforms.SessionDetail{Session: &db.Session{}}},
		{name: "missing summary", wantErr: true},
		{name: "unreachable summary", err: platforms.ErrPlatformUnreachable, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &fakePlatform{sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return tc.detail, tc.err }}
			parent, err := permissionPolicyParent(t.Context(), adapter, "s")
			if parent != tc.wantParent || (err != nil) != tc.wantErr {
				t.Fatalf("parent = %q, error = %v", parent, err)
			}
			if tc.name == "missing summary" && !errors.Is(err, platforms.ErrNotFound) {
				t.Fatalf("missing summary did not fail closed: %v", err)
			}
		})
	}
}
