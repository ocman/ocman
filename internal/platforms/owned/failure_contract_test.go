package owned

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type failedHistoryNative struct{ *historyPlatform }

func (*failedHistoryNative) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	return nil, platforms.ErrPlatformUnreachable
}
func (*failedHistoryNative) HistorySnapshot(context.Context, string) (*db.HistorySnapshot, error) {
	return nil, platforms.ErrPlatformUnreachable
}
func (*failedHistoryNative) HistorySequences(context.Context) (map[string]int64, error) {
	return nil, platforms.ErrPlatformUnreachable
}
func (*failedHistoryNative) HistoryMessages(context.Context, string, []string) (*db.HistorySnapshot, error) {
	return nil, platforms.ErrPlatformUnreachable
}

func TestOwnerHistoryFailuresAreNotPublishedAsEmptySnapshots(t *testing.T) {
	a, p := historyFixture(t)
	row, err := a.historyIdentity(t.Context(), "hidden-child")
	if err != nil {
		t.Fatal(err)
	}
	a.native = &failedHistoryNative{p}
	for name, read := range map[string]func() error{
		"inventory": func() error { _, err := a.HistoryInventory(t.Context()); return err },
		"snapshot":  func() error { _, err := a.HistorySnapshot(t.Context(), row.ID); return err },
		"sequences": func() error { _, err := a.HistorySequences(t.Context()); return err },
		"messages":  func() error { _, err := a.HistoryMessages(t.Context(), row.ID, nil); return err },
	} {
		if err := read(); !errors.Is(err, platforms.ErrPlatformUnreachable) {
			t.Errorf("%s swallowed owner failure: %v", name, err)
		}
	}
}

func TestCanceledCatalogueReadsNeverReachNativeExecution(t *testing.T) {
	a, p, _ := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, read := range map[string]func() error{
		"canonical discovery": func() error { _, err := a.canonical(ctx, "native"); return err },
		"history discovery":   func() error { _, err := a.historyIdentity(ctx, "native"); return err },
		"runtime lifecycle":   func() error { _, err := a.GetSessionLifecycle(ctx, row.ID); return err },
		"dispose":             func() error { return a.DisposeSession(ctx, platforms.DisposeSessionRequest{SessionID: row.ID}) },
		"commit":              func() error { return a.CommitCreation(ctx, row.ID) },
		"fork": func() error {
			_, err := a.ForkSession(ctx, platforms.ForkSessionRequest{SessionID: row.ID})
			return err
		},
		"create": func() error {
			_, err := a.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "cancelled"}})
			return err
		},
	} {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s lost cancellation: %v", name, err)
		}
	}
	if p.creates != 0 {
		t.Fatal("cancelled catalogue read allocated a native session")
	}
}

func TestOptionalMutationsRequireSupportAndPreserveDisposalFences(t *testing.T) {
	a, p, store := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func() error{
		"revert":       func() error { return a.Revert(t.Context(), platforms.RevertSessionRequest{SessionID: row.ID}) },
		"unrevert":     func() error { return a.Unrevert(t.Context(), platforms.UnrevertSessionRequest{SessionID: row.ID}) },
		"native queue": func() error { _, err := a.NativeQueued(t.Context(), row.ID); return err },
		"cancel native queue": func() error {
			return a.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{SessionID: row.ID})
		},
		"dispose": func() error { return a.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: row.ID}) },
	} {
		if err := mutate(); !errors.Is(err, platforms.ErrUnsupported) {
			t.Errorf("%s invented support: %v", name, err)
		}
	}
	if err := a.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: "native"}); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatal(err)
	}
	native := &optionalPlatform{nativePlatform: p, errorOnDispose: platforms.ErrPlatformUnreachable}
	a.native = native
	if err := a.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: row.ID}); !errors.Is(err, native.errorOnDispose) {
		t.Fatal(err)
	}
	if persisted, err := store.Get(t.Context(), row.ID); err != nil || persisted.Purged {
		t.Fatalf("uncertain disposal scrubbed identity: %+v %v", persisted, err)
	}
	reservation, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "configuring-disposal", PayloadHash: strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Attach(t.Context(), reservation.ID, a.binding("unpublished-native")); err != nil {
		t.Fatal(err)
	}
	native.errorOnDispose = platforms.ErrNotFound
	if err := a.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: reservation.ID}); err != nil {
		t.Fatal(err)
	}
	if persisted, err := store.Get(t.Context(), reservation.ID); err != nil || persisted.CreationState != sessioncatalog.CreationFailed {
		t.Fatalf("disposed unpublished creation could replay: %+v %v", persisted, err)
	}
}
