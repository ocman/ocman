package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type nativePermissionPlatform struct {
	*nativePlatform
	id string
}

func (*nativePermissionPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{NativeAutoPermission: true}
}
func (p *nativePermissionPlatform) NativePermissionState(_ context.Context, id string) (*platforms.NativePermissionState, error) {
	p.id = id
	return &platforms.NativePermissionState{Mode: "auto"}, nil
}

func TestNativePermissionReadUsesNativeIdentity(t *testing.T) {
	a, native, _ := fixture(t)
	publish(t, a, native.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.NativePermissionState(t.Context(), rows[0].ID); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	p := &nativePermissionPlatform{nativePlatform: native}
	a.native = p
	state, err := a.NativePermissionState(t.Context(), rows[0].ID)
	if err != nil || state.Mode != "auto" || p.id != "native" {
		t.Fatalf("state=%+v id=%q err=%v", state, p.id, err)
	}
	if _, err := a.NativePermissionState(t.Context(), "ocm_00000000-0000-0000-0000-000000000000"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
}
