package platformconnections

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestNativeConfigurationDuplicateSaveRejected(t *testing.T) {
	for _, kind := range []string{"claudecode", "codex"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := openStore(t)
			initialize(t, s)
			first, err := s.Reserve(t.Context(), kind, "existing", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.Reserve(t.Context(), kind, "existing", first.Directory)
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.SaveReservation(t.Context(), first.ID, "First", true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveReservation(t.Context(), second.ID, "Second", true); !errors.Is(err, ErrConfigurationOwned) {
				t.Errorf("duplicate native configuration save: %v", err)
			}
			if err := s.Remove(t.Context(), c.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveReservation(t.Context(), second.ID, "Second", true); !errors.Is(err, ErrConfigurationOwned) {
				t.Errorf("removed connection lost native configuration ownership: %v", err)
			}
			if r, err := s.Reservation(t.Context(), second.ID); err != nil || r.Status != "pending" {
				t.Fatalf("conflict consumed receipt: %+v %v", r, err)
			}
		})
	}
}

func TestNativeConfigurationCanonicalAliases(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	directory := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "not-created/config"} {
		first, err := s.Create(t.Context(), Input{Kind: "codex", Name: "first", Configuration: "existing", Directory: filepath.Join(directory, suffix)})
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Reserve(t.Context(), "codex", "existing", filepath.Join(alias, suffix))
		if err != nil {
			t.Fatal(err)
		}
		if r.Directory != filepath.Join(alias, suffix) || first.Directory != filepath.Join(directory, suffix) {
			t.Fatalf("owner-pinned paths changed: %q %q", r.Directory, first.Directory)
		}
		if _, err := s.SaveReservation(t.Context(), r.ID, "second", true); !errors.Is(err, ErrConfigurationOwned) {
			t.Fatalf("alias save = %v", err)
		}
		if _, err := s.Create(t.Context(), Input{Kind: "codex", Name: "direct", Configuration: "isolated", Directory: r.Directory}); !errors.Is(err, ErrConfigurationOwned) {
			t.Fatalf("direct create bypassed ownership: %v", err)
		}
	}
	// OpenCode can share its configuration directory; its physical sources are
	// independently isolated by the connection-aware native runtime.
	for range 2 {
		if _, err := s.Create(t.Context(), Input{Kind: "opencode", Name: "shared", Configuration: "existing", Directory: directory}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeConfigurationConcurrentClaimsAcrossHandles(t *testing.T) {
	path := statetest.Path(t, Schema)
	var stores []*Store
	for range 2 {
		db, err := state.Open(path, Schema)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		stores = append(stores, New(db))
	}
	initialize(t, stores[0])
	directory := t.TempDir()
	var reservations []Reservation
	for range 8 {
		r, err := stores[0].Reserve(t.Context(), "claudecode", "existing", directory)
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, r)
	}
	errs := make(chan error, len(reservations))
	var wg sync.WaitGroup
	for i, r := range reservations {
		wg.Go(func() { _, err := stores[i%2].SaveReservation(t.Context(), r.ID, "account", true); errs <- err })
	}
	wg.Wait()
	close(errs)
	saved := 0
	for err := range errs {
		if err == nil {
			saved++
		} else if !errors.Is(err, ErrConfigurationOwned) {
			t.Error(err)
		}
	}
	if saved != 1 {
		t.Fatalf("saved %d owners of one physical configuration", saved)
	}
}

func TestNativeConfigurationMigrationValidatesRetainedOwnership(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonicalize", true: "reject duplicate tombstone"}[duplicate], func(t *testing.T) {
			previous := Schema
			previous.Migrations = previous.Migrations[:len(previous.Migrations)-1]
			path := filepath.Join(t.TempDir(), "state.db")
			directory := t.TempDir()
			directory, err := filepath.EvalSymlinks(directory)
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(directory, alias); err != nil {
				t.Fatal(err)
			}
			db, err := state.Open(path, previous)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL().ExecContext(t.Context(), `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled) VALUES('first','codex','first','existing',?,0)`, alias); err != nil {
				t.Fatal(err)
			}
			if err := New(db).BindSource(t.Context(), "first", "codex", "physical-source"); err != nil {
				t.Fatal(err)
			}
			if duplicate {
				if _, err := db.SQL().ExecContext(t.Context(), `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled,removed) VALUES('removed','codex','removed','existing',?,0,1)`, directory); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = state.Open(path, Schema)
			if duplicate {
				if db != nil {
					_ = db.Close()
				}
				if err == nil || !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "removed") || !strings.Contains(err.Error(), "reconcile ownership") {
					t.Fatalf("migration conflict = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c, err := New(db).Get(t.Context(), "first")
			if err != nil || c.Directory != alias {
				t.Fatalf("migrated configuration = %+v %v", c, err)
			}
			var physical string
			if err := db.SQL().QueryRowContext(t.Context(), `SELECT native_directory FROM platform_connection WHERE id='first'`).Scan(&physical); err != nil || physical != directory {
				t.Fatalf("migrated physical identity = %q %v", physical, err)
			}
			if id, err := New(db).SourceConnection(t.Context(), "codex", "physical-source"); err != nil || id != "first" {
				t.Fatalf("migration changed physical source ownership: %q %v", id, err)
			}
		})
	}
}

func TestNativeConfigurationRejectsUnresolvablePaths(t *testing.T) {
	directory := t.TempDir()
	dangling := filepath.Join(directory, "dangling")
	if err := os.Symlink(filepath.Join(directory, "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(directory, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative", dangling, filepath.Join(dangling, "config"), loop} {
		if _, err := canonicalConfiguration("codex", path); !errors.Is(err, ErrInvalid) {
			t.Errorf("unresolvable physical configuration %q accepted: %v", path, err)
		}
	}
}
