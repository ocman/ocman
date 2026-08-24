package autoapprove

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// recordingStore captures RecordApprovedPermission calls for the
// capture tests. All other SettingsStore methods return zero values so
// the pipeline treats settings as absent.
type recordingStore struct {
	mu      sync.Mutex
	records []recordedApproval
	called  chan error
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
func (s *recordingStore) RecordApprovedPermission(ctx context.Context, platform, sessionID string, p state.ApprovedPermission) error {
	s.mu.Lock()
	s.records = append(s.records, recordedApproval{platform, sessionID, p})
	s.mu.Unlock()
	if s.called != nil {
		s.called <- ctx.Err()
	}
	return nil
}

func (s *recordingStore) snapshot() []recordedApproval {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedApproval(nil), s.records...)
}

func waitForRecords(t *testing.T, store *recordingStore, want int) []recordedApproval {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if records := store.snapshot(); len(records) == want {
			return records
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("recorded %d rows, want %d", len(store.snapshot()), want)
	return nil
}

// TestHandlePermissionReplied_CapturesAlways proves that an
// asked -> replied flow persists user approvals with the original
// permission text/patterns, while rejects persist nothing.
func TestHandlePermissionReplied_CapturesAlways(t *testing.T) {
	tests := []struct {
		name      string
		reply     string
		wantRows  int
		wantPerm  string
		wantPats  []string
		wantReply string
	}{
		{
			name:      "always writes exactly one row",
			reply:     "always",
			wantRows:  1,
			wantPerm:  "bash",
			wantPats:  []string{"git *"},
			wantReply: "always",
		},
		{
			name:      "once writes exactly one row",
			reply:     "once",
			wantRows:  1,
			wantPerm:  "bash",
			wantPats:  []string{"git *"},
			wantReply: "once",
		},
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
			s.rememberAsked(string(platforms.ID("opencode")), "ses-1", "perm-1", "bash", []string{"git *"}, map[string]any{"command": "git status"})

			s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", tc.reply)

			records := waitForRecords(t, store, tc.wantRows)
			if len(records) != tc.wantRows {
				t.Fatalf("recorded %d rows, want %d", len(records), tc.wantRows)
			}
			if tc.wantRows == 0 {
				return
			}
			rec := records[0]
			if rec.platform != "opencode" || rec.sessionID != "ses-1" {
				t.Errorf("row scoped to (%q,%q), want (opencode,ses-1)", rec.platform, rec.sessionID)
			}
			if rec.perm.PermissionText != tc.wantPerm {
				t.Errorf("PermissionText = %q, want %q", rec.perm.PermissionText, tc.wantPerm)
			}
			if len(rec.perm.Patterns) != len(tc.wantPats) || (len(tc.wantPats) > 0 && rec.perm.Patterns[0] != tc.wantPats[0]) {
				t.Errorf("Patterns = %v, want %v", rec.perm.Patterns, tc.wantPats)
			}
			if rec.perm.ApprovedBy != "user" || rec.perm.Reply != tc.wantReply || rec.perm.Reasoning != "" {
				t.Errorf("provenance = (%q,%q,%q)", rec.perm.ApprovedBy, rec.perm.Reply, rec.perm.Reasoning)
			}
			if !reflect.DeepEqual(rec.perm.Metadata, map[string]any{"command": "git status"}) || rec.perm.AskedAt == 0 {
				t.Errorf("asked snapshot = metadata %#v at %d", rec.perm.Metadata, rec.perm.AskedAt)
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
	if len(store.snapshot()) != 0 {
		t.Fatalf("recorded %d rows, want 0", len(store.snapshot()))
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
	s.rememberAsked("opencode", "ses-1", "perm-1", "edit", []string{"*.go"}, nil)
	s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", "always")
	s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", "always")
	if records := waitForRecords(t, store, 1); len(records) != 1 {
		t.Fatalf("recorded %d rows, want 1 (asked entry must be consumed once)", len(records))
	}
}

func TestHandlePermissionReplied_EmitsUserApproval(t *testing.T) {
	store := &recordingStore{}
	s := &Service{
		deps:        Deps{Store: store},
		autoApprove: make(map[string]*autoApproveStatus),
		askedCache:  make(map[string]askedPermission),
		sseSessions: make(map[string]map[*Sink]struct{}),
	}
	buf := &bytes.Buffer{}
	flushed := make(chan struct{}, 1)
	s.RegisterSink("ses-1", buf, func() { flushed <- struct{}{} })
	s.rememberAsked("opencode", "ses-1", "perm-1", "bash", []string{"pnpm test"}, map[string]any{"command": "pnpm test"})

	s.HandlePermissionReplied(t.Context(), "ses-1", "perm-1", "once")
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("user approval event was not emitted")
	}

	got := buf.String()
	if !strings.Contains(got, "event: ocman.permission.approved") || !strings.Contains(got, `"approvedBy":"user"`) {
		t.Fatalf("user approval event = %q", got)
	}
	data := strings.Split(strings.TrimSpace(got), "data: ")
	var payload map[string]any
	if len(data) != 2 || json.Unmarshal([]byte(data[1]), &payload) != nil {
		t.Fatalf("decode event: %q", got)
	}
	for _, field := range []string{"approvedBy", "reply", "metadata", "askedAt", "approvedAt"} {
		if _, ok := payload[field]; !ok {
			t.Errorf("event missing %s: %#v", field, payload)
		}
	}
}

func TestRememberAskedPreservesFirstSnapshot(t *testing.T) {
	s := &Service{askedCache: make(map[string]askedPermission)}
	patterns := []string{"git *"}
	metadata := map[string]any{"command": "git status", "nested": map[string]any{"value": "first"}}
	first := s.rememberAsked("opencode", "s1", "p1", "bash", patterns, metadata)
	patterns[0] = "mutated"
	metadata["command"] = "mutated"
	metadata["nested"].(map[string]any)["value"] = "mutated"
	second := s.rememberAsked("other", "s1", "p1", "edit", []string{"*"}, map[string]any{"path": "later"})
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("duplicate snapshot = %#v, want first %#v", second, first)
	}
	if second.patterns[0] != "git *" || second.metadata["command"] != "git status" || second.metadata["nested"].(map[string]any)["value"] != "first" {
		t.Fatalf("snapshot mutated through caller values: %#v", second)
	}
}

func TestAIResponseEventIsNotCapturedAsUser(t *testing.T) {
	store := &recordingStore{}
	s := &Service{deps: Deps{Store: store}, autoApprove: make(map[string]*autoApproveStatus), askedCache: make(map[string]askedPermission)}
	asked := s.rememberAsked("opencode", "s1", "p1", "bash", nil, nil)
	s.recordJudged("s1", "p1", verdictSafe)
	adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		s.HandlePermissionReplied(t.Context(), "s1", "p1", "once")
		return nil
	}}

	s.respondAndPersistSafeApproval("opencode", adapter, "s1", "p1", asked, "safe", log.NewEntry(log.StandardLogger()))
	records := waitForRecords(t, store, 1)
	if records[0].perm.ApprovedBy != "ai" {
		t.Fatalf("approval source = %q, want ai", records[0].perm.ApprovedBy)
	}
}

func TestFailedAIResponseDoesNotGuessObservedReplySource(t *testing.T) {
	store := &recordingStore{}
	s := &Service{deps: Deps{Store: store}, autoApprove: make(map[string]*autoApproveStatus), askedCache: make(map[string]askedPermission)}
	asked := s.rememberAsked("opencode", "s1", "p1", "bash", nil, nil)
	s.recordJudged("s1", "p1", verdictSafe)
	adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error {
		s.HandlePermissionReplied(t.Context(), "s1", "p1", "always")
		return errors.New("already answered")
	}}

	s.respondAndPersistSafeApproval("opencode", adapter, "s1", "p1", asked, "safe", log.NewEntry(log.StandardLogger()))
	time.Sleep(20 * time.Millisecond)
	if len(store.snapshot()) != 0 {
		t.Fatalf("recorded ambiguous approval as %#v", store.snapshot())
	}
}

func TestDirectUserSuccessCapturedDespiteSafeVerdict(t *testing.T) {
	store := &recordingStore{}
	s := &Service{deps: Deps{Store: store}, autoApprove: make(map[string]*autoApproveStatus), askedCache: make(map[string]askedPermission)}
	s.rememberAsked("opencode", "s1", "p1", "bash", nil, nil)
	s.recordJudged("s1", "p1", verdictSafe)

	s.HandleDirectPermissionReply(t.Context(), "s1", "p1", "once")
	records := waitForRecords(t, store, 1)
	if records[0].perm.ApprovedBy != "user" {
		t.Fatalf("approval source = %q, want user", records[0].perm.ApprovedBy)
	}
}

func TestUserReplyPersistenceDetachesCanceledContext(t *testing.T) {
	store := &recordingStore{called: make(chan error, 1)}
	s := &Service{deps: Deps{Store: store}, autoApprove: make(map[string]*autoApproveStatus), askedCache: make(map[string]askedPermission)}
	s.rememberAsked("opencode", "s1", "p1", "bash", nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	s.HandleDirectPermissionReply(ctx, "s1", "p1", "always")
	select {
	case persistErr := <-store.called:
		if persistErr != nil {
			t.Fatalf("persistence context error = %v, want nil", persistErr)
		}
	case <-time.After(time.Second):
		t.Fatal("persistence was not called")
	}
}
