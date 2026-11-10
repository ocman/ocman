package codextransport

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNamedCodexConstructorRejectsMissingAndChangedOwnerState(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{"missing-owner-state", "requires owner state"},
		{"missing-row", "not found"},
		{"changed-home", "persisted home differs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			connection := storedConnection(t, r, t.TempDir())
			cfg := Config{State: r.store, DataDir: r.data, Connection: connection}
			switch tc.name {
			case "missing-owner-state":
				cfg.State = nil
			case "missing-row":
				connection.ID = "cnx_" + uuid.NewString()
			case "changed-home":
				connection.Home = t.TempDir()
			}
			p, err := newPlatform(t.Context(), cfg, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
			if p != nil {
				p.Close()
			}
			if p != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("platform=%v, error=%v", p, err)
			}
		})
	}
}

func TestNamedCodexInspectionUsesSelectedPlatformCLI(t *testing.T) {
	r := newRig(t)
	connection := storedConnection(t, r, t.TempDir())
	p, err := newPlatform(t.Context(), Config{State: r.store, DataDir: r.data, Connection: connection}, launcher{exe: r.exe, cli: filepath.Join(t.TempDir(), "absent"), acp: r.exe})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got, err := p.InspectConnection(t.Context())
	if err != nil || got.Ready || got.IsolationSupported || !strings.Contains(got.Message, "version inspection failed") {
		t.Fatalf("inspection=%+v, %v", got, err)
	}
}
