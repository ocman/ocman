package resourcemod

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestResourceSessionLinks(t *testing.T) {
	for _, tc := range []struct {
		name, command, want string
		failed              bool
	}{
		{"known", "agent --session-id native", "canonical", false},
		{"resume", "agent --resume=native", "canonical", false},
		{"session", "agent --session native", "canonical", false},
		{"fork overrides resume source", "agent --resume native --fork-session --session-id fork-native", "fork", false},
		{"fork with unavailable source", "agent --resume unknown --fork-session --session-id=fork-native", "fork", false},
		{"explicit identity before resume", "agent --session-id fork-native --resume native", "fork", false},
		{"session overrides resume", "agent --resume unknown --session native", "canonical", false},
		{"missing value", "agent --session-id", "", false},
		{"option is not a value", "agent --resume --verbose", "", false},
		{"unknown", "agent --session-id unknown", "", false},
		{"not an identity flag", "echo native", "", false},
		{"ambiguous", "agent --resume duplicate", "", false},
		{"purged", "agent --session-id purged", "", false},
		{"failure", "agent --session-id native", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Module{Sessions: func(_ context.Context, owner string, limit, offset int) ([]sessioncatalog.Session, error) {
				if owner != "local" || limit != 500 || offset != 0 {
					t.Fatal("lost owner or pagination")
				}
				if tc.failed {
					return nil, errors.New("unavailable")
				}
				return []sessioncatalog.Session{
					{ID: "canonical", NativeID: "native"},
					{ID: "fork", NativeID: "fork-native"},
					{ID: "one", NativeID: "duplicate"},
					{ID: "two", NativeID: "duplicate"},
					{ID: "gone", NativeID: "purged", Purged: true},
				}, nil
			}}
			original := []hostsvc.ResourceProcess{{PID: 1, Command: tc.command}, {PID: 2, ParentPID: 1}}
			got := m.sessionLinks(t.Context(), "local", original)
			if got[0].SessionID != tc.want || got[1].SessionID != tc.want {
				t.Fatalf("links=%+v", got)
			}
			if original[0].SessionID != "" {
				t.Fatal("mutated host snapshot")
			}
		})
	}
}

func TestResourceSessionLinkPaginationAndCycles(t *testing.T) {
	m := &Module{Sessions: func(_ context.Context, _ string, _, offset int) ([]sessioncatalog.Session, error) {
		if offset == 0 {
			return make([]sessioncatalog.Session, 500), nil
		}
		return []sessioncatalog.Session{{ID: "canonical", NativeID: "native"}}, nil
	}}
	processes := []hostsvc.ResourceProcess{
		{PID: 1, Command: "agent --session-id native"},
		{PID: 2, ParentPID: 3}, {PID: 3, ParentPID: 2},
		{PID: 4, ParentPID: 1, Command: "agent --session-id unknown"},
	}
	got := m.sessionLinks(t.Context(), "local", processes)
	if got[0].SessionID != "canonical" || got[1].SessionID != "" || got[2].SessionID != "" || got[3].SessionID != "" {
		t.Fatal(got)
	}
}
