package owned

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

// The fake platform has its own durable database, independent of Ocman's writer.
// Only the test binary is launched. No agent or live database is used.
type crashCreationNative struct {
	*nativePlatform
	durable *sql.DB
	stage   string
}

func (*crashCreationNative) PlannedSessionID(id string) string {
	return "ses_" + strings.TrimPrefix(id, "ocm_")
}

func (p *crashCreationNative) pause(stage string) {
	if p.stage == stage {
		fmt.Println("creation-crash-ready")
		<-time.After(time.Minute) // parent kills the process, not a graceful shutdown
	}
}

func (p *crashCreationNative) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.pause("before-post")
	if _, err := p.durable.ExecContext(ctx, `INSERT INTO native_session(id,directory) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, req.NativeID, req.Directory); err != nil {
		return nil, err
	}
	p.pause("lost-response")
	return &platforms.CreateSessionResponse{ID: req.NativeID}, nil
}

func (p *crashCreationNative) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	row := &db.Session{ID: id}
	err := p.durable.QueryRowContext(ctx, `SELECT directory FROM native_session WHERE id=?`, id).Scan(&row.Directory)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platforms.ErrNotFound
	}
	return row, err
}

func crashCreationAdapter(t *testing.T, root, stage string) (*Adapter, *sql.DB) {
	t.Helper()
	core, err := state.Open(filepath.Join(root, "state.db"), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	store, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	history, err := statev2.Open(filepath.Join(root, "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	durable, err := sql.Open("sqlite", filepath.Join(root, "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = durable.Close() })
	if _, err := durable.Exec(`CREATE TABLE IF NOT EXISTS native_session(id TEXT PRIMARY KEY,directory TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	native := &crashCreationNative{nativePlatform: &nativePlatform{id: "test", source: "source"}, durable: durable, stage: stage}
	a, err := New(t.Context(), native, store, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	return a, durable
}

func crashCreationRequest() platforms.CreateSessionRequest {
	return platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{Origin: "user", RequestKey: "hard-kill"}}
}

func TestPlannedCreationCrashChild(t *testing.T) {
	root := os.Getenv("OCMAN_TEST_CREATION_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	a, _ := crashCreationAdapter(t, root, os.Getenv("OCMAN_TEST_CREATION_CRASH_STAGE"))
	if _, err := a.CreateSession(t.Context(), crashCreationRequest()); err != nil {
		t.Fatal(err)
	}
	t.Fatal("parent did not kill the creation process")
}

func TestPlannedCreationSurvivesHardKill(t *testing.T) {
	for _, stage := range []string{"before-post", "lost-response"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPlannedCreationCrashChild$")
			cmd.Env = append(os.Environ(), "OCMAN_TEST_CREATION_CRASH_ROOT="+root, "OCMAN_TEST_CREATION_CRASH_STAGE="+stage)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			ready := make(chan bool, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					if scanner.Text() == "creation-crash-ready" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("child exited before the crash boundary")
				}
			case <-ctx.Done():
				t.Fatal("child did not reach the crash boundary")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("child exited normally instead of being killed")
			}
			a, native := crashCreationAdapter(t, root, "")
			rows, err := a.store.List(t.Context(), "owner", 10, 0)
			if err != nil || len(rows) != 1 || rows[0].NativeID == "" || rows[0].CreationState != sessioncatalog.Configuring {
				t.Fatalf("hard kill lost the native plan: %+v %v", rows, err)
			}
			response, err := a.CreateSession(t.Context(), crashCreationRequest())
			if err != nil || response.ID != rows[0].ID {
				t.Fatalf("hard-kill recovery: %+v %v", response, err)
			}
			if err := a.CommitCreation(t.Context(), response.ID); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := native.QueryRow(`SELECT count(*) FROM native_session`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("recovery duplicated native sessions: %d %v", count, err)
			}
		})
	}
}
