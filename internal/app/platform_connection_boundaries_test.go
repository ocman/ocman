package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestConnectionManagedStoreInventoryAndDeletionAreAccountScoped(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	var accounts []platformconnections.Connection
	for _, name := range []string{"one", "two"} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: name, Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, c)
	}
	first, err := platformconnections.NewRuntimeStore(t.Context(), core, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := platformconnections.NewRuntimeStore(t.Context(), core, accounts[1])
	if err != nil {
		t.Fatal(err)
	}
	a, b := connectionManagedStore{first}, connectionManagedStore{second}
	for _, root := range []string{"/one", "/two"} {
		if err := a.Upsert(t.Context(), root, local.ManagedInstance{Endpoint: "http://127.0.0.1:1", Kind: "native-tmux", RuntimeID: root, PID: 123, LaunchedAt: time.Unix(5, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Upsert(t.Context(), "/sibling", local.ManagedInstance{RuntimeID: "sibling"}); err != nil {
		t.Fatal(err)
	}
	rows, err := a.List(t.Context())
	if err != nil || len(rows) != 2 || rows["/one"].PID != 123 || rows["/one"].LaunchedAt.Unix() != 5 {
		t.Fatalf("scoped inventory: %+v %v", rows, err)
	}
	if err := a.Delete(t.Context(), "/one"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.Get(t.Context(), "/one"); err != nil || ok {
		t.Fatal(ok, err)
	}
	if rows, err := b.List(t.Context()); err != nil || len(rows) != 1 {
		t.Fatal("deleted sibling runtime", rows, err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.List(t.Context()); err == nil {
		t.Fatal("closed inventory became empty success")
	}
	if err := a.Delete(t.Context(), "/two"); err == nil {
		t.Fatal("closed delete succeeded")
	}
}

func TestConnectionIdleUsesNativeInventoryAndOwnerQueue(t *testing.T) {
	for _, mode := range []string{"idle", "busy", "prompt", "read-error", "own-queue", "legacy-queue", "sibling-queue", "state-error"} {
		t.Run(mode, func(t *testing.T) {
			core := statetest.Open(t, platformconnections.Schema)
			p := &platformtest.Fake{PlatformID: "claudecode", SessionList: []db.Session{{ID: "native", Status: db.StatusDone}}}
			switch mode {
			case "busy":
				p.SessionList[0].Status = db.StatusBusy
			case "prompt":
				p.SessionList[0].PendingQuestion = true
			case "read-error":
				p.SessionsErr = errors.New("native unavailable")
			case "own-queue", "legacy-queue", "sibling-queue":
				platform := map[string]string{"own-queue": "cnx_work", "legacy-queue": "claudecode", "sibling-queue": "r-remote:claudecode"}[mode]
				if err := core.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "queued", Platform: platform, SessionID: "session", Text: "held"}); err != nil {
					t.Fatal(err)
				}
			case "state-error":
				if err := core.Close(); err != nil {
					t.Fatal(err)
				}
			}
			idle, err := connectionIdle(t.Context(), core, "cnx_work", p)
			wantError := mode == "read-error" || mode == "own-queue" || mode == "legacy-queue" || mode == "state-error"
			if (err != nil) != wantError {
				t.Fatalf("error=%v", err)
			}
			wantIdle := mode == "idle" || mode == "sibling-queue"
			if idle != wantIdle {
				t.Fatalf("idle=%v want=%v", idle, wantIdle)
			}
			if mode == "read-error" {
				if err := waitConnectionIdle(t.Context(), core, "cnx_work", p); err == nil {
					t.Fatal("native failure treated as safe detach")
				}
			}
		})
	}
}

func TestLegacyOpenCodeConfigurationKeepsOriginalPathsAndPrivateRuntime(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "xdg"}[explicit], func(t *testing.T) {
			for _, key := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
				value := ""
				if explicit {
					value = filepath.Join(t.TempDir(), key)
				}
				t.Setenv(key, value)
			}
			f := connectionFactory{dataDir: t.TempDir(), openCodeDB: filepath.Join(t.TempDir(), "native.db")}
			c := platformconnections.Connection{ID: "cnx_original", Kind: "opencode", LegacyKind: "opencode", Directory: t.TempDir()}
			cfg := f.openCodeConfig(c)
			if cfg.DBPath != f.openCodeDB || cfg.ConfigDir != c.Directory || cfg.RuntimeDir != filepath.Join(f.dataDir, "platform-connections", c.ID, "native", "runtime") {
				t.Fatalf("legacy paths rewritten: %+v", cfg)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(home, ".local", "share")
			if explicit {
				want = os.Getenv("XDG_DATA_HOME")
			}
			if cfg.DataDir != want {
				t.Fatal(cfg.DataDir, want)
			}
		})
	}
}

func TestConnectionFactoryStorageErrorsDoNotPublishAdapter(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	f := connectionFactory{state: core, dataDir: t.TempDir(), openCodeDB: filepath.Join(t.TempDir(), "not-service.db")}
	c := platformconnections.Connection{ID: "cnx_legacy", Kind: "opencode", LegacyKind: "opencode", Directory: t.TempDir()}
	auth, err := f.connectionAuth(t.Context(), c)
	if err != nil || !auth.Enabled() {
		t.Fatal("legacy generated auth missing", err)
	}
	file := filepath.Join(t.TempDir(), "not-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f.dataDir = file
	if _, err := f.openCode(t.Context(), c); err == nil {
		t.Fatal("published adapter with unusable runtime storage")
	}
	f.dataDir = t.TempDir()
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.connectionAuth(t.Context(), c); err == nil {
		t.Fatal("legacy secret store failure ignored")
	}
	c.LegacyKind = ""
	if _, err := f.connectionAuth(t.Context(), c); err == nil {
		t.Fatal("named secret store failure ignored")
	}
	if _, _, err := f.buildCollection(t.Context(), platformconnections.New(core)); err == nil {
		t.Fatal("unreadable collection became empty success")
	}
	if _, _, err := blockedConnectionKinds(t.Context(), platformconnections.New(core)); err == nil {
		t.Fatal("unreadable intent skipped version policy")
	}
}
