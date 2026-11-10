package sessionsvc

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestNamedPlatformKindKeepsAccountSelection(t *testing.T) {
	r := newConnectionRig(t)
	for _, connection := range r.connections {
		kind, err := r.svc.PlatformKind(t.Context(), connection.ID)
		if err != nil || kind != platforms.OpenCodeID {
			t.Fatalf("model namespace for %s: %s %v", connection.ID, kind, err)
		}
	}
	remote := addNamedRemote(t, r, "ocm_model_namespace")
	if kind, err := r.svc.PlatformKind(t.Context(), string(remote.ID())); err != nil || kind != platforms.OpenCodeID {
		t.Fatalf("remote routing ID leaked into model namespace: %s %v", kind, err)
	}
	if _, err := r.svc.PlatformKind(t.Context(), "cnx_missing"); err == nil {
		t.Fatal("missing account borrowed the default model namespace")
	}
}
