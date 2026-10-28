package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type optionalPlatform struct {
	*nativePlatform
	permissionErr, errorOnDispose error
	disposed                      []platforms.DisposeSessionRequest
	rules                         []platforms.SetPermissionRulesRequest
}

func (p *optionalPlatform) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	p.rules = append(p.rules, req)
	return p.permissionErr
}
func (p *optionalPlatform) DisposeSession(_ context.Context, req platforms.DisposeSessionRequest) error {
	p.disposed = append(p.disposed, req)
	return p.errorOnDispose
}
func (p *optionalPlatform) SessionLifecycle(_ context.Context, id string) (*platforms.SessionLifecycle, error) {
	if id != "native" {
		return nil, platforms.ErrNotFound
	}
	return &platforms.SessionLifecycle{LatestMessageID: "native-message", LatestMessageRole: "assistant"}, nil
}
func (p *optionalPlatform) RefreshPermissions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	if id != "native" {
		return nil, platforms.ErrNotFound
	}
	return []platforms.LivePrompt{{"id": "permission", "sessionID": id, "metadata": map[string]any{"sessionId": "opaque-native"}}}, nil
}

func TestOptionalInterfacesKeepOnlyCanonicalReferencesOutsidePlatform(t *testing.T) {
	a, native, store := fixture(t)
	publish(t, a, native.rows...)
	a.native = &optionalPlatform{nativePlatform: native}
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	lifecycle, err := a.SessionLifecycle(t.Context(), id)
	if err != nil || lifecycle.LatestMessageID != "native-message" {
		t.Fatalf("message identity changed: %+v %v", lifecycle, err)
	}
	prompts, err := a.RefreshPermissions(t.Context(), id)
	if err != nil || prompts[0]["sessionID"] != id || prompts[0]["id"] != "permission" || prompts[0]["metadata"].(map[string]any)["sessionId"] != "opaque-native" {
		t.Fatalf("prompt references: %+v %v", prompts, err)
	}
	if err := a.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	row, err := store.Get(t.Context(), id)
	if err != nil || !row.Purged {
		t.Fatalf("disposed identity was recycled: %+v %v", row, err)
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("disposed identity regained execution: %v", err)
	}
}

func TestConfigurationFailureKeepsCanonicalRecoveryAndNativeBindingPrivate(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "disposed", true: "uncertain cleanup"}[cleanupFails], func(t *testing.T) {
			a, native, store := fixture(t)
			p := &optionalPlatform{nativePlatform: native, permissionErr: errors.New("rules rejected")}
			if cleanupFails {
				p.errorOnDispose = errors.New("cleanup uncertain")
			}
			a.native = p
			_, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "configured", Origin: "user", Configured: true}})
			if !errors.Is(err, p.permissionErr) {
				t.Fatal(err)
			}
			var pending *CreationPendingError
			if errors.As(err, &pending) != cleanupFails {
				t.Fatalf("wrong allocation certainty: %v", err)
			}
			rows, readErr := store.List(t.Context(), "owner", 10, 0)
			if readErr != nil || len(rows) != 1 || rows[0].NativeID != "created-native" {
				t.Fatalf("native recovery binding lost: %+v %v", rows, readErr)
			}
			want := sessioncatalog.CreationFailed
			if cleanupFails {
				want = sessioncatalog.Configuring
			}
			if rows[0].CreationState != want || len(p.rules) != 1 || p.rules[0].SessionID != "created-native" || len(p.disposed) != 1 || p.disposed[0].SessionID != "created-native" {
				t.Fatalf("configuration leaked canonical ID into native platform: %+v %+v %+v", rows, p.rules, p.disposed)
			}
		})
	}
}
