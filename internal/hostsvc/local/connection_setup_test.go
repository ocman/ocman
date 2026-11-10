package local

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func connectionHost(t *testing.T) (*Host, *platformconnections.Store) {
	t.Helper()
	db := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(db)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{StateDir: t.TempDir(), ConnectionStore: store, ConnectionConfigurations: map[string]hostsvc.ConnectionConfiguration{
		"codex":      {ExistingDirectory: filepath.Join(t.TempDir(), "existing-codex"), SeparateConfiguration: true},
		"claudecode": {ExistingDirectory: filepath.Join(t.TempDir(), "existing-claude")},
	}})
	return h, store
}

func TestConnectionSetupPrepareRetryAndSave(t *testing.T) {
	h, store := connectionHost(t)
	request := hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "isolated"}
	r, err := h.PreparePlatformConnection(t.Context(), request)
	if err != nil || r.ID == "" || r.ConnectionID == "" || r.Kind != "codex" || r.Status != "pending" {
		t.Fatalf("prepare: %+v %v", r, err)
	}
	for path := r.Directory; path != h.deps.StateDir; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("not a private configuration directory: %s %+v %v", path, info, err)
		}
	}
	entries, err := os.ReadDir(r.Directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preparation copied account data: %v %v", entries, err)
	}
	// A new Host stands in for restart. The receipt, not current defaults or
	// a repeated Add request, pins where the user's login wrote credentials.
	h = New(h.deps)
	for range 2 {
		got, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID})
		if err != nil || got != r {
			t.Fatalf("retry: %+v %v", got, err)
		}
	}
	c, err := h.SavePlatformConnection(t.Context(), r.ID, "Codex Work", true)
	if err != nil || c.ID != r.ConnectionID || c.Directory != r.Directory || c.Name != "Codex Work" {
		t.Fatalf("save changed prepared identity: %+v %v", c, err)
	}
	if _, err := store.Default(t.Context()); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatalf("save silently selected a default: %v", err)
	}
	second, err := h.PreparePlatformConnection(t.Context(), request)
	if err != nil || second.ID == r.ID || second.Directory == r.Directory {
		t.Fatalf("same-kind accounts share configuration: %+v %v", second, err)
	}
	if err := store.Remove(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatalf("resumed a removed connection: %v", err)
	}
}

func TestConnectionSetupExistingDoesNotMutateNativeDirectory(t *testing.T) {
	h, _ := connectionHost(t)
	existing := h.deps.ConnectionConfigurations["codex"].ExistingDirectory
	r, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "existing"})
	if err != nil || r.Directory != existing {
		t.Fatalf("existing configuration: %+v %v", r, err)
	}
	if _, err := os.Lstat(existing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("existing native configuration was created/changed: %v", err)
	}
	// Changing the integration's default must not retarget this receipt.
	h.deps.ConnectionConfigurations["codex"] = hostsvc.ConnectionConfiguration{ExistingDirectory: "/another-account"}
	got, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID})
	if err != nil || got != r {
		t.Fatalf("receipt followed changed default: %+v %v", got, err)
	}
	if _, err := h.SavePlatformConnection(t.Context(), r.ID, "Later", false); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionSetupFailsClosed(t *testing.T) {
	h, store := connectionHost(t)
	for _, req := range []hostsvc.ConnectionSetupRequest{
		{Kind: "unknown", Configuration: "existing"},
		{Kind: "claudecode", Configuration: "isolated"},
	} {
		if _, err := h.PreparePlatformConnection(t.Context(), req); !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("unsupported setup: %+v %v", req, err)
		}
	}
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "invalid"}); !errors.Is(err, platformconnections.ErrInvalid) {
		t.Fatalf("invalid mode: %v", err)
	}
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: "missing"}); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatalf("missing receipt fell back: %v", err)
	}
	r, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "isolated"})
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []hostsvc.ConnectionSetupRequest{
		{ReservationID: r.ID, Kind: "claudecode"},
		{ReservationID: r.ID, Configuration: "existing"},
	} {
		if _, err := h.PreparePlatformConnection(t.Context(), req); !errors.Is(err, platformconnections.ErrInvalid) {
			t.Fatalf("retargeted reservation: %v", err)
		}
	}
	delete(h.deps.ConnectionConfigurations, "codex")
	if _, err := h.SavePlatformConnection(t.Context(), r.ID, "No longer supported", true); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("save ignored withdrawn support: %v", err)
	}
	if err := h.CancelPlatformConnectionSetup(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.Directory); err != nil {
		t.Fatalf("cancel deleted possible login credentials: %v", err)
	}
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatalf("resumed canceled setup: %v", err)
	}
	if got, err := store.Read(t.Context()); err != nil || len(got.Connections) != 0 {
		t.Fatalf("failed setup created connection: %+v %v", got, err)
	}
	h.deps.ConnectionStore = nil
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := h.CancelPlatformConnectionSetup(t.Context(), r.ID); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestConnectionSetupPrivateDirectoryRefusals(t *testing.T) {
	for _, part := range []string{"platform-connections", "account", "config"} {
		for _, mode := range []string{"symlink", "file", "public"} {
			t.Run(part+"/"+mode, func(t *testing.T) {
				h, store := connectionHost(t)
				id := uuid.NewString()
				directory := filepath.Join(h.deps.StateDir, "platform-connections", id, "config")
				parts := map[string]string{
					"platform-connections": filepath.Join(h.deps.StateDir, "platform-connections"),
					"account":              filepath.Join(h.deps.StateDir, "platform-connections", id),
					"config":               directory,
				}
				target := parts[part]
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "symlink":
					if err := os.Symlink(t.TempDir(), target); err != nil {
						t.Fatal(err)
					}
				case "file":
					if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "public":
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(target, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				r, err := store.Reserve(t.Context(), "codex", "isolated", directory)
				if err != nil {
					t.Fatal(err)
				}
				got, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID})
				if err == nil || got.ID != r.ID {
					t.Fatalf("accepted unsafe directory or lost receipt: %+v %v", got, err)
				}
				if got, err := store.Reservation(t.Context(), r.ID); err != nil || !reflect.DeepEqual(got, r) {
					t.Fatalf("failed preparation lost resumable state: %+v %v", got, err)
				}
			})
		}
	}
}

func TestConnectionSetupRejectsUnreservedPaths(t *testing.T) {
	h, store := connectionHost(t)
	for _, path := range []string{
		filepath.Join(t.TempDir(), "config"), h.deps.StateDir,
		filepath.Join(h.deps.StateDir, "platform-connections", "not-a-uuid", "config"),
		filepath.Join(h.deps.StateDir, "platform-connections", uuid.NewString(), "other"),
	} {
		r, err := store.Reserve(t.Context(), "codex", "isolated", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID}); !errors.Is(err, platformconnections.ErrInvalid) {
			t.Fatalf("accepted unreserved path %s: %v", path, err)
		}
	}
	h.deps.StateDir = ""
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "isolated"}); err == nil {
		t.Fatal("fell back to shared state directory")
	}
}

func TestConnectionSetupDoesNotRecreateSavedConfiguration(t *testing.T) {
	h, _ := connectionHost(t)
	r, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "isolated"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.SavePlatformConnection(t.Context(), r.ID, "Account", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("silently replaced a saved configuration: %v", err)
	}
	if _, err := os.Lstat(r.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saved configuration was recreated: %v", err)
	}
}

func TestConnectionSetupConcurrentPreparationsKeepEnvironment(t *testing.T) {
	h, _ := connectionHost(t)
	before := os.Environ()
	var wg sync.WaitGroup
	results := make(chan platformconnections.Reservation, 16)
	for range 16 {
		wg.Go(func() {
			r, err := New(h.deps).PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "isolated"})
			if err != nil {
				t.Errorf("parallel preparation: %v", err)
			}
			results <- r
		})
	}
	wg.Wait()
	close(results)
	directories := map[string]bool{}
	for r := range results {
		if r.ID == "" || directories[r.Directory] {
			t.Fatalf("parallel accounts shared configuration: %+v", r)
		}
		directories[r.Directory] = true
	}
	if !reflect.DeepEqual(os.Environ(), before) {
		t.Fatal("preparation changed the process-global environment")
	}
}

func TestConnectionSetupPreservesOwnerPathsWithCanonicalOwnership(t *testing.T) {
	for _, mode := range []string{"existing", "isolated"} {
		t.Run(mode, func(t *testing.T) {
			h, store := connectionHost(t)
			physicalRoot := h.deps.StateDir
			ownerRoot := filepath.Join(t.TempDir(), "owner-state")
			if err := os.Symlink(physicalRoot, ownerRoot); err != nil {
				t.Fatal(err)
			}
			h.deps.StateDir = ownerRoot
			existing := filepath.Join(ownerRoot, "existing")
			if err := os.Mkdir(existing, 0o700); err != nil {
				t.Fatal(err)
			}
			h.deps.ConnectionConfigurations["codex"] = hostsvc.ConnectionConfiguration{ExistingDirectory: existing, SeparateConfiguration: true}
			r, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: mode})
			if err != nil {
				t.Fatal(err)
			}
			if rel, err := filepath.Rel(ownerRoot, r.Directory); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || (mode == "existing" && r.Directory != existing) {
				t.Fatalf("receipt changed owner-pinned directory: %+v rel=%q err=%v", r, rel, err)
			}
			physical, err := filepath.EvalSymlinks(r.Directory)
			if err != nil || physical == r.Directory {
				t.Fatalf("test requires different owner/physical path spellings: %q %q %v", r.Directory, physical, err)
			}
			c, err := h.SavePlatformConnection(t.Context(), r.ID, "Account", false)
			if err != nil || c.ID != r.ConnectionID || c.Directory != r.Directory {
				t.Fatalf("save changed prepared identity: %+v %v", c, err)
			}
			h = New(h.deps)
			resumed, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{ReservationID: r.ID})
			if err != nil || resumed.ID != r.ID || resumed.ConnectionID != r.ConnectionID || resumed.Directory != r.Directory || resumed.Status != "saved" {
				t.Fatalf("restart changed pinned receipt: %+v %v", resumed, err)
			}
			duplicate, err := store.Reserve(t.Context(), "codex", "existing", physical)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SaveReservation(t.Context(), duplicate.ID, "Alias", false); !errors.Is(err, platformconnections.ErrConfigurationOwned) {
				t.Fatalf("physical alias bypassed exclusive ownership: %v", err)
			}
		})
	}
}
