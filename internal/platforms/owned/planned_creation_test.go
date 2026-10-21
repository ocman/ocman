package owned

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
)

type plannedNative struct {
	*nativePlatform
	loseResponse bool
	before       func(platforms.CreateSessionRequest)
}

func TestNativeCreationPlanCannotBeSuppliedOverTheWire(t *testing.T) {
	var req platforms.CreateSessionRequest
	if err := json.Unmarshal([]byte(`{"NativeID":"ses_foreign","nativeID":"ses_foreign","Directory":"/repo"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.NativeID != "" {
		t.Fatal("wire request chose an integration-local native identity")
	}
	req.NativeID = "ses_private"
	raw, err := json.Marshal(req)
	if err != nil || strings.Contains(string(raw), "ses_private") {
		t.Fatalf("native plan leaked into request fingerprint/wire: %s %v", raw, err)
	}
}

func (p *plannedNative) PlannedSessionID(id string) string {
	return "ses_" + strings.TrimPrefix(id, "ocm_")
}

func (p *plannedNative) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if p.before != nil {
		p.before(req)
	}
	p.creates++
	if req.NativeID == "" {
		return nil, errors.New("creation has no durable native identity")
	}
	p.rows = append(p.rows, db.Session{ID: req.NativeID, Directory: req.Directory})
	if p.loseResponse {
		return nil, context.DeadlineExceeded
	}
	return &platforms.CreateSessionResponse{ID: req.NativeID}, nil
}

func TestPlannedCreationRecoversLostResponseAfterDatabaseReopen(t *testing.T) {
	fixture, native, _ := fixture(t)
	p := &plannedNative{nativePlatform: native, loseResponse: true}
	path := filepath.Join(t.TempDir(), "state.db")
	var current *state.DB
	open := func() *Adapter {
		t.Helper()
		var err error
		current, err = state.Open(path, sessioncatalog.Schema)
		if err != nil {
			t.Fatal(err)
		}
		store, err := sessioncatalog.New(current)
		if err != nil {
			t.Fatal(err)
		}
		a, err := New(t.Context(), p, store, fixture.history, "owner")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := open()
	t.Cleanup(func() { _ = current.Close() })
	p.before = func(req platforms.CreateSessionRequest) {
		row, err := a.store.FindBinding(t.Context(), a.binding(req.NativeID))
		if err != nil || row.CreationState != sessioncatalog.Configuring {
			t.Fatalf("native side effect preceded durable binding: %+v %v", row, err)
		}
	}
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "lost-response"}}
	_, err := a.CreateSession(t.Context(), req)
	var pending *CreationPendingError
	if !errors.As(err, &pending) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost response: %v", err)
	}
	id := pending.SessionID
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	a = open()
	p.loseResponse = false
	response, err := a.CreateSession(t.Context(), req)
	if err != nil || response.ID != id || response.Replayed || p.creates != 1 {
		t.Fatalf("recovery failed or duplicated allocation: %+v creates=%d %v", response, p.creates, err)
	}
	if err := a.CommitCreation(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	response, err = a.CreateSession(t.Context(), req)
	if err != nil || !response.Replayed || response.ID != id || p.creates != 1 {
		t.Fatalf("completed receipt replay: %+v creates=%d %v", response, p.creates, err)
	}
}

func TestPlannedCreationCanResumeBeforeNativeAllocation(t *testing.T) {
	a, native, _ := fixture(t)
	p := &plannedNative{nativePlatform: native}
	a.native = p
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "before-post"}}
	row, _, err := a.reserve(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.planCreation(t.Context(), row, &req); err != nil {
		t.Fatal(err)
	}
	// All adapter memory is discarded after the write-ahead binding, before POST.
	a, err = New(t.Context(), p, a.store, a.history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	response, err := a.CreateSession(t.Context(), req)
	if err != nil || response.ID != row.ID || p.creates != 1 {
		t.Fatalf("pre-allocation recovery: %+v creates=%d %v", response, p.creates, err)
	}
}

func TestPlannedCreationRejectsUnexpectedNativeResult(t *testing.T) {
	a, native, _ := fixture(t)
	p := &plannedNative{nativePlatform: native}
	a.native = p
	row, _, err := a.reserve(t.Context(), platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "wrong-result"}})
	if err != nil {
		t.Fatal(err)
	}
	req := platforms.CreateSessionRequest{}
	row, err = a.planCreation(t.Context(), row, &req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.finishNativeCreation(t.Context(), row, &platforms.CreateSessionResponse{ID: "unexpected"}, nil, &platforms.CreationIdentity{}); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
		t.Fatalf("different native ID replaced durable plan: %v", err)
	}
}

type plannedProbe struct {
	*plannedNative
	metadata *db.Session
	probeErr error
}

func (p *plannedProbe) SessionIdentityMetadata(context.Context, string) (*db.Session, error) {
	return p.metadata, p.probeErr
}

func TestPlannedRecoveryFailsClosedOnUnusableIdentityProof(t *testing.T) {
	for _, kind := range []string{"unavailable", "missing metadata", "wrong ID", "wrong directory"} {
		t.Run(kind, func(t *testing.T) {
			a, native, _ := fixture(t)
			p := &plannedProbe{plannedNative: &plannedNative{nativePlatform: native}}
			a.native = p
			req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "proof"}}
			row, _, err := a.reserve(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			row, err = a.planCreation(t.Context(), row, &req)
			if err != nil {
				t.Fatal(err)
			}
			p.metadata = &db.Session{ID: row.NativeID, Directory: row.Directory}
			want := sessioncatalog.ErrInvalid
			switch kind {
			case "unavailable":
				p.probeErr = platforms.ErrPlatformUnreachable
				want = p.probeErr
			case "missing metadata":
				p.metadata = nil
			case "wrong ID":
				p.metadata.ID = "foreign"
			case "wrong directory":
				p.metadata.Directory = "/foreign"
			}
			if _, err := a.CreateSession(t.Context(), req); !errors.Is(err, want) || p.creates != 0 {
				t.Fatalf("unproven result recovered: creates=%d %v", p.creates, err)
			}
		})
	}
}

type configuredPlannedNative struct {
	*plannedNative
	rules []platforms.SetPermissionRulesRequest
}

func (p *configuredPlannedNative) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	p.rules = append(p.rules, req)
	return nil
}

func (p *configuredPlannedNative) DisposeSession(context.Context, platforms.DisposeSessionRequest) error {
	return nil
}

func TestPlannedRecoveryReappliesConfigurationBeforePublication(t *testing.T) {
	a, native, _ := fixture(t)
	p := &configuredPlannedNative{plannedNative: &plannedNative{nativePlatform: native}}
	a.native = p
	rules := []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "configured", Configured: true, PermissionRules: rules}}
	first, err := a.CreateSession(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Stop before application linkage and Bind, then discard adapter memory.
	a, err = New(t.Context(), p, a.store, a.history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := a.CreateSession(t.Context(), req)
	if err != nil || recovered.ID != first.ID || recovered.Replayed || !recovered.ConfigurationApplied || p.creates != 1 || len(p.rules) != 2 {
		t.Fatalf("configuration recovery: %+v creates=%d rules=%v %v", recovered, p.creates, p.rules, err)
	}
	if p.rules[1].SessionID != p.PlannedSessionID(first.ID) || p.rules[1].Rules[0] != rules[0] {
		t.Fatalf("recovery changed initial rules: %+v", p.rules[1])
	}
	if _, err := a.nativeID(t.Context(), first.ID); !errors.Is(err, sessioncatalog.ErrCreating) {
		t.Fatalf("recovered creation became executable before application linkage: %v", err)
	}
}
