package statev2

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only transient files (statement journals, temporary tables and sorter
// spills) live in memory. Committed history stays in the synchronous WAL.
func TestWriterKeepsDurableJournalWithMemoryTempStore(t *testing.T) {
	d := openTestDB(t)
	for pragma, want := range map[string]string{"temp_store": "2", "journal_mode": "wal", "synchronous": "2"} {
		var got string
		if err := d.db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
			t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
		}
	}
}

const crashChildEnv = "STATEV2_CRASH_CHILD_DB"

// A process killed mid-transaction must keep every committed batch and none of
// the uncommitted one, even after its dirty pages spilled into the WAL.
func TestCrashKeepsCommittedHistory(t *testing.T) {
	if path := os.Getenv(crashChildEnv); path != "" {
		crashChild(path)
		return
	}
	path := filepath.Join(t.TempDir(), "statev2.db")
	child := exec.Command(os.Args[0], "-test.run=^TestCrashKeepsCommittedHistory$", "-test.count=1")
	child.Env = append(os.Environ(), crashChildEnv+"="+path)
	child.Stderr = os.Stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "crash-child:") {
				if line != "crash-child: ready" {
					ready <- fmt.Errorf("%s", line)
				} else {
					ready <- nil
				}
				return
			}
		}
		ready <- fmt.Errorf("child exited before the uncommitted transaction: %w", scanner.Err())
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = child.Process.Kill()
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		_ = child.Process.Kill()
		t.Fatal("child did not reach the uncommitted transaction")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var integrity string
	if err := d.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
	var committed, uncommitted int
	if err := d.db.QueryRow(`SELECT count(*) FROM message WHERE session_id='session'`).Scan(&committed); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT count(*) FROM message WHERE session_id='uncommitted'`).Scan(&uncommitted); err != nil {
		t.Fatal(err)
	}
	if committed != 1 || uncommitted != 0 {
		t.Fatalf("after crash: committed messages = %d, uncommitted = %d", committed, uncommitted)
	}
	if _, err := d.ReadConversation(t.Context(), Key{InstanceID: "first", SessionID: "session"}, 0, 0); err != nil {
		t.Fatalf("committed conversation unreadable after crash: %v", err)
	}
}

// crashChild commits one batch, then holds a large write transaction open
// until the parent kills the process. A tiny page cache forces uncommitted
// pages into the WAL so recovery must discard them.
func crashChild(path string) {
	fail := func(err error) {
		fmt.Printf("crash-child: %v\n", err)
		os.Exit(1)
	}
	d, err := Open(path)
	if err != nil {
		fail(err)
	}
	if err := d.RegisterInstance(context.Background(), Instance{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "first"}); err != nil {
		fail(err)
	}
	if _, err := d.Commit(context.Background(), fixture("first")); err != nil {
		fail(err)
	}
	if _, err := d.db.Exec("PRAGMA cache_size=8"); err != nil {
		fail(err)
	}
	tx, err := d.db.Begin()
	if err != nil {
		fail(err)
	}
	if _, err := tx.Exec(`INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent)
		VALUES('first','uncommitted','','',1,1,0,1,'{}','','',0,0)`); err != nil {
		fail(err)
	}
	payload := strings.Repeat("x", 4096)
	for i := range 2000 {
		if _, err := tx.Exec(`INSERT INTO message(instance_id,session_id,id,position,role,model,created_at,completed_at,source_deleted,extensions,
			provider,cost,tokens_input,tokens_output,tokens_reasoning,tokens_cache_read,tokens_cache_write,finish,has_error,error_name,agent,conversation_visible)
			VALUES('first','uncommitted',?,?,'assistant','',1,1,0,?,'',0,0,0,0,0,0,'',0,'','',1)`, fmt.Sprint(i), i, `{"pad":"`+payload+`"}`); err != nil {
			fail(err)
		}
	}
	// Statement journals are needed only inside a still-open transaction.
	if _, err := tx.Exec(`UPDATE message SET source_deleted=1 WHERE instance_id='first' AND session_id='uncommitted'`); err != nil {
		fail(err)
	}
	wal, err := os.Stat(path + "-wal")
	if err != nil {
		fail(err)
	}
	if wal.Size() < 1<<20 {
		fail(fmt.Errorf("uncommitted pages did not spill into the WAL: %d bytes", wal.Size()))
	}
	fmt.Println("crash-child: ready")
	select {}
}
