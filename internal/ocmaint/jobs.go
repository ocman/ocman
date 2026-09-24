package ocmaint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Gate refuses opencode launches while a job runs.
type Gate struct{ reason atomic.Pointer[string] }

// Err returns an error while the gate is closed.
func (g *Gate) Err() error {
	if r := g.reason.Load(); r != nil {
		return errors.New(*r)
	}
	return nil
}

func (g *Gate) close(reason string) { g.reason.Store(&reason) }
func (g *Gate) open()               { g.reason.Store(nil) }

func (r *Runner) backupPath() string { return r.deps.DBPath + ".ocman-backup" }

// dbBytes sums the database and its WAL.
func dbBytes(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	n := st.Size()
	if wal, err := os.Stat(path + "-wal"); err == nil {
		n += wal.Size()
	}
	return n, nil
}

// needSpace fails unless dir's volume has need bytes free.
func (j *jobRun) needSpace(dir string, need int64) (string, error) {
	free, err := j.r.deps.FreeBytes(dir)
	if err != nil {
		return "", err
	}
	if need < 0 || free < uint64(need) {
		return "", fmt.Errorf("need %s free on %s, have %s", gb(need), dir, gb(int64(free))) //nolint:gosec // free fits
	}
	return fmt.Sprintf("%s free, %s needed", gb(int64(free)), gb(need)), nil //nolint:gosec // free fits
}

func gb(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/1e9) }

func (r *Runner) cleanup(ctx context.Context, j *jobRun) error {
	d := r.deps
	if err := j.step("Check disk space", func() (string, error) {
		size, err := dbBytes(d.DBPath)
		if err != nil {
			return "", err
		}
		// ponytail: worst case, not an estimate. The backup is one copy;
		// the dump plus the compacted copy VACUUM writes through the WAL
		// can't exceed another; the strip's WAL gets the third.
		return j.needSpace(filepath.Dir(d.DBPath), 3*size)
	}); err != nil {
		return err
	}
	if err := j.step("Stop opencode", func() (string, error) { return j.stopAll(ctx) }); err != nil {
		return err
	}
	db, conn, err := openWritable(ctx, d.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	defer conn.Close()

	backup := r.backupPath()
	if err := j.step("Back up database", func() (string, error) {
		_ = os.Remove(backup) // VACUUM INTO refuses an existing file
		if err := backupTo(ctx, conn, backup); err != nil {
			return "", err
		}
		return backup, nil
	}); err != nil {
		return err
	}
	if err := j.step("Dump diffs", func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(d.DumpPath), 0o700); err != nil {
			return "", err
		}
		if err := attachDump(ctx, conn, d.DumpPath); err != nil {
			return "", err
		}
		cutoff := time.Now().Add(-CutoffAge).UnixMilli()
		msgs, events, err := dumpDiffs(ctx, conn, cutoff)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d message(s), %d event(s) to %s", msgs, events, d.DumpPath), nil
	}); err != nil {
		return err
	}
	if err := j.step("Remove diffs", func() (string, error) {
		if err := stripDiffs(ctx, conn); err != nil {
			return "", err
		}
		j.changed()
		return "", checkpoint(ctx, conn)
	}); err != nil {
		return err
	}
	if err := j.step("Compact database", func() (string, error) {
		before, _ := dbBytes(d.DBPath)
		if _, err := conn.ExecContext(ctx, `DETACH DATABASE dump`); err != nil {
			return "", err
		}
		if err := compact(ctx, conn); err != nil {
			return "", err
		}
		after, _ := dbBytes(d.DBPath)
		return fmt.Sprintf("%s → %s", gb(before), gb(after)), nil
	}); err != nil {
		return err
	}
	return j.step("Delete backup", func() (string, error) { return "", os.Remove(backup) })
}

func (r *Runner) restore(ctx context.Context, j *jobRun) error {
	d := r.deps
	if err := j.step("Check disk space", func() (string, error) {
		st, err := os.Stat(d.DumpPath)
		if err != nil {
			return "", err
		}
		// The restored rows land in the WAL before they reach the file.
		return j.needSpace(filepath.Dir(d.DBPath), 2*st.Size())
	}); err != nil {
		return err
	}
	if err := j.step("Stop opencode", func() (string, error) { return j.stopAll(ctx) }); err != nil {
		return err
	}
	db, conn, err := openWritable(ctx, d.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	defer conn.Close()
	return j.step("Restore diffs", func() (string, error) {
		if err := attachDump(ctx, conn, d.DumpPath); err != nil {
			return "", err
		}
		if err := restoreDiffs(ctx, conn); err != nil {
			return "", err
		}
		j.changed()
		return "", checkpoint(ctx, conn)
	})
}
