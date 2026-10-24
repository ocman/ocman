package inbox

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type badgeRemotes struct {
	Remotes
	sources []string
	items   func(context.Context, string) ([]state.InboxItem, error)
}

func (r badgeRemotes) InboxSources() []string { return r.sources }
func (r badgeRemotes) InboxItems(ctx context.Context, source string) ([]state.InboxItem, error) {
	return r.items(ctx, source)
}

func TestBadgeCountLocalItems(t *testing.T) {
	db := statetest.Open(t)
	for _, category := range []string{state.InboxGeneral, state.InboxFactory, state.InboxRoutine} {
		if _, err := db.CreateSessionInboxItem(t.Context(), category, "message", category, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnsurePermissionInboxItem(t.Context(), state.InboxPermission{Platform: "fake", SessionID: "s", PermissionID: "p", Permission: "Bash"}); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: db})
	count, err := m.BadgeCount(t.Context(), map[string]bool{state.InboxGeneral: true, state.InboxFactory: true, state.InboxPermissionCategory: true})
	if err != nil || count != 2 {
		t.Fatalf("badge = %d, %v; want 2 without permission duplication", count, err)
	}
}

func TestBadgeCountOwnersAndUnavailableSources(t *testing.T) {
	for _, failingSource := range []string{"", "local", "remote"} {
		t.Run(failingSource, func(t *testing.T) {
			r := badgeRemotes{sources: []string{"local", "remote"}, items: func(_ context.Context, source string) ([]state.InboxItem, error) {
				if source == failingSource {
					return nil, errors.New("unavailable")
				}
				return []state.InboxItem{
					{ID: "same"}, {ID: "read", ReadAt: 1}, {ID: "archived", ArchivedAt: 1},
					{ID: "legacy-permission", Permission: &state.InboxPermission{}},
				}, nil
			}}
			m := NewModule(Deps{Remotes: func() Remotes { return r }})
			count, err := m.BadgeCount(t.Context(), map[string]bool{state.InboxGeneral: true})
			if failingSource == "local" {
				if err == nil {
					t.Fatal("local storage failure ignored")
				}
				return
			}
			want := 2
			if failingSource == "remote" {
				want = 1
			}
			if err != nil || count != want {
				t.Fatalf("badge = %d, %v; want %d", count, err, want)
			}
		})
	}
}
