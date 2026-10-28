package provider

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestHistoryConfirmationCapabilityAndRoute(t *testing.T) {
	for _, supported := range []bool{false, true} {
		caps := allCapabilities()
		caps.HistoryConfirm = supported
		f := newFake(t, caps)
		a := f.adapter(t)
		err := a.ConfirmHistory(t.Context(), "child", providerprotocol.HistoryConfirmation{Entries: map[string]string{"m:message": "present"}})
		if a.SupportsHistoryConfirmation() != supported {
			t.Fatal("capability mismatch")
		}
		if !supported {
			if !errors.Is(err, platforms.ErrUnsupported) {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		call := f.calls[len(f.calls)-1]
		if call.method != "POST" || call.path != "/sessions/child/history-confirmed" {
			t.Fatalf("wrong route: %+v", call)
		}
	}
}
