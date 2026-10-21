package owned

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func TestCreationReservesCanonicalIdentityBeforeNativeAllocation(t *testing.T) {
	a, p, store := fixture(t)
	p.beforeCreate = func(platforms.CreateSessionRequest) {
		rows, err := store.List(t.Context(), "owner", 10, 0)
		if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.Creating || rows[0].NativeID != "" || rows[0].OriginID != "routine" {
			t.Fatalf("native allocation preceded canonical intent: %+v %v", rows, err)
		}
	}
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "start", Origin: "routine", OriginID: "routine"}}
	created, err := a.CreateSession(t.Context(), req)
	if err != nil || !sessioncatalog.IsID(created.ID) {
		t.Fatalf("create returned native identity: %+v %v", created, err)
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID}); !errors.Is(err, sessioncatalog.ErrCreating) {
		t.Fatalf("unpublished creation acquired execution authority: %v", err)
	}
	if err := a.CommitCreation(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateSession(t.Context(), req); err != nil || p.creates != 1 {
		t.Fatalf("replay allocated another native session: %d %v", p.creates, err)
	}
}

func TestCreationReplaysReceiptWrittenBeforeCanonicalAdapterMigration(t *testing.T) {
	a, p, store := fixture(t)
	req := platforms.CreateSessionRequest{Directory: "/repo", Port: "runtime-port", Creation: &platforms.CreationIdentity{RequestKey: "legacy-key", Origin: "user"}}
	intent := req
	intent.Port = ""
	// This is the exact pre-cutover sessionsvc receipt envelope. It is a
	// compatibility fixture, not a call to the new fingerprint implementation.
	raw, err := json.Marshal(struct {
		Request   platforms.CreateSessionRequest
		Rules     []platforms.PermissionRule
		Operation []any
	}{Request: intent})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	row, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "legacy-key", PayloadHash: hex.EncodeToString(hash[:])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bind(t.Context(), row.ID, a.binding("legacy-native")); err != nil {
		t.Fatal(err)
	}
	response, err := a.CreateSession(t.Context(), req)
	if err != nil || response.ID != row.ID || p.creates != 0 {
		t.Fatalf("legacy keyed creation did not replay: %+v %d %v", response, p.creates, err)
	}
}

func TestForkRetainsCanonicalParentAndNativeForkPoint(t *testing.T) {
	a, p, store := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	parent := rows[0].ID
	req := platforms.ForkSessionRequest{SessionID: parent, MessageID: "native-message", Creation: &platforms.CreationIdentity{RequestKey: "fork", Origin: "user"}}
	response, err := a.ForkSession(t.Context(), req)
	if err != nil || !sessioncatalog.IsID(response.ID) {
		t.Fatalf("fork: %+v %v", response, err)
	}
	row, err := store.Get(t.Context(), response.ID)
	if err != nil || row.ParentID != parent || row.NativeID != "forked-native" || row.CreationState != sessioncatalog.Configuring || p.forkRequest.SessionID != "native" || p.forkRequest.MessageID != "native-message" {
		t.Fatalf("fork identity: %+v %+v %v", row, p.forkRequest, err)
	}
	if err := a.CommitCreation(t.Context(), response.ID); err != nil {
		t.Fatal(err)
	}
	again, err := a.ForkSession(t.Context(), req)
	if err != nil || again.ID != response.ID || p.creates != 1 {
		t.Fatalf("fork replay: %+v %d %v", again, p.creates, err)
	}
	if req.Creation.ParentID != "" || req.SessionID != parent {
		t.Fatal("fork mutated caller intent")
	}
}

func TestDefinitiveRejectionPreventsAnotherNativeAllocation(t *testing.T) {
	a, p, store := fixture(t)
	p.createErr = platforms.ErrCreationRejected
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "rejected", Origin: "user"}}
	if _, err := a.CreateSession(t.Context(), req); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	if _, err := a.CreateSession(t.Context(), req); !errors.Is(err, sessioncatalog.ErrCreationFailed) || p.creates != 1 {
		t.Fatalf("rejected intent retried allocation: %d %v", p.creates, err)
	}
	rows, err := store.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.CreationFailed {
		t.Fatalf("rejected receipt: %+v %v", rows, err)
	}
}

func TestUnknownCreationOutcomeKeepsOnlyCanonicalRecoveryHandle(t *testing.T) {
	a, p, store := fixture(t)
	p.createErr = context.DeadlineExceeded
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "start", Origin: "user"}}
	_, err := a.CreateSession(t.Context(), req)
	var pending *CreationPendingError
	if !errors.As(err, &pending) || !sessioncatalog.IsID(pending.SessionID) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing canonical recovery handle: %v", err)
	}
	if _, err := a.CreateSession(t.Context(), req); !errors.Is(err, sessioncatalog.ErrCreating) || p.creates != 1 {
		t.Fatalf("uncertain request replayed: %d %v", p.creates, err)
	}
	rows, err := store.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.Creating {
		t.Fatalf("uncertain receipt lost: %+v %v", rows, err)
	}
}

func TestConfiguredCreationRejectsMissingDisposalBeforeAllocating(t *testing.T) {
	a, p, _ := fixture(t)
	_, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", Configured: true}})
	if !errors.Is(err, platforms.ErrUnsupported) || p.creates != 0 {
		t.Fatalf("unsupported configuration allocated a session: %d %v", p.creates, err)
	}
}
