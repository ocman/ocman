package autoapprove

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// recordingStore captures RecordApprovedPermission calls for the
// capture tests. All other SettingsStore methods return zero values so
// the pipeline treats settings as absent.
type recordingStore struct {
	records []recordedApproval
}

type recordedApproval struct {
	platform  string
	sessionID string
	perm      state.ApprovedPermission
}

func (s *recordingStore) GetAutoApprove(context.Context, string, string) (bool, bool, error) {
	return false, false, nil
}
func (s *recordingStore) GetJudgeDelayMs(context.Context) (int64, error) { return 0, nil }
func (s *recordingStore) GetPromptSections(context.Context) ([]state.PromptSection, error) {
	return nil, nil
}
func (s *recordingStore) GetSetting(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (s *recordingStore) RecordApprovedPermission(_ context.Context, platform, sessionID string, p state.ApprovedPermission) error {
	s.records = append(s.records, recordedApproval{platform, sessionID, p})
	return nil
}

// TestHandlePermissionReplied_CapturesAlways proves that an
// asked -> replied("always") flow persists exactly one approval row
// with the original permission text/patterns, while "once" and
// "reject" persist nothing. Regression test for issue #101 step 1.
func TestHandlePermissionReplied_CapturesAlways(t *testing.T) {
	tests := []struct {
		name       string
		reply      string
		wantRows   int
		wantPerm   string
		wantPats   []string
		wantReason string
	}{
		{
			name:       "always writes exactly one row",
			reply:      "always",
			wantRows:   1,
			wantPerm:   "bash",
			wantPats:   []string{"git *"},
			wantReason: "user clicked Allow always",
		},
		{name: "once writes no row", reply: "once", wantRows: 0},
		{name: "reject writes no row", reply: "reject", wantRows: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingStore{}
			s := &Service{
				deps:        Deps{Store: store},
				autoApprove: make(map[string]*autoApproveStatus),
				askedCache:  make(map[string]askedPermission),
			}

			// Simulate the asked-side cache population that Ensure does.
			s.rememberAsked(string(platforms.ID("opencode")), "ses-1", "perm-1", "bash", []string{"git *"})

			s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", tc.reply)

			if len(store.records) != tc.wantRows {
				t.Fatalf("recorded %d rows, want %d", len(store.records), tc.wantRows)
			}
			if tc.wantRows == 0 {
				return
			}
			rec := store.records[0]
			if rec.platform != "opencode" || rec.sessionID != "ses-1" {
				t.Errorf("row scoped to (%q,%q), want (opencode,ses-1)", rec.platform, rec.sessionID)
			}
			if rec.perm.PermissionText != tc.wantPerm {
				t.Errorf("PermissionText = %q, want %q", rec.perm.PermissionText, tc.wantPerm)
			}
			if len(rec.perm.Patterns) != len(tc.wantPats) || (len(tc.wantPats) > 0 && rec.perm.Patterns[0] != tc.wantPats[0]) {
				t.Errorf("Patterns = %v, want %v", rec.perm.Patterns, tc.wantPats)
			}
			if rec.perm.Reasoning != tc.wantReason {
				t.Errorf("Reasoning = %q, want %q", rec.perm.Reasoning, tc.wantReason)
			}
			if rec.perm.JudgeSessionID != "" {
				t.Errorf("JudgeSessionID = %q, want empty", rec.perm.JudgeSessionID)
			}
		})
	}
}

// TestHandlePermissionReplied_AlwaysWithoutAskedData verifies a
// "always" reply with no cached asked-side data persists nothing
// (ocman may have started after the prompt was shown).
func TestHandlePermissionReplied_AlwaysWithoutAskedData(t *testing.T) {
	store := &recordingStore{}
	s := &Service{
		deps:        Deps{Store: store},
		autoApprove: make(map[string]*autoApproveStatus),
		askedCache:  make(map[string]askedPermission),
	}
	s.HandlePermissionReplied(t.Context(), "ses-1", "perm-unknown", "always")
	if len(store.records) != 0 {
		t.Fatalf("recorded %d rows, want 0", len(store.records))
	}
}

// TestHandlePermissionReplied_TakesAskedOnce verifies the asked-cache
// entry is consumed (evicted) on reply so a duplicate replied event
// cannot double-record.
func TestHandlePermissionReplied_TakesAskedOnce(t *testing.T) {
	store := &recordingStore{}
	s := &Service{
		deps:        Deps{Store: store},
		autoApprove: make(map[string]*autoApproveStatus),
		askedCache:  make(map[string]askedPermission),
	}
	s.rememberAsked("opencode", "ses-1", "perm-1", "edit", []string{"*.go"})
	s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", "always")
	s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", "always")
	if len(store.records) != 1 {
		t.Fatalf("recorded %d rows, want 1 (asked entry must be consumed once)", len(store.records))
	}
}
