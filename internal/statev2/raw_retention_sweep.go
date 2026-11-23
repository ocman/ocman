package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const rawSweepBudget = 30 * time.Second

const rawSweepProgressSchema = `CREATE TABLE IF NOT EXISTS raw_sweep_progress (
	id INTEGER PRIMARY KEY CHECK(id=1), cutoff INTEGER NOT NULL, after_row INTEGER NOT NULL,
	last_row INTEGER NOT NULL, next_run INTEGER NOT NULL)`

type rawSweepProgress struct{ cutoff, after, last, nextRun int64 }

type RawHistorySweep struct {
	Messages  int64
	Snapshots int64
	Bytes     int64 // payload reduction, not SQLite file size
	Pending   bool  // more work remains in this pass
}

// SweepRawHistory runs a time-bounded slice and saves each page's progress.
func (d *DB) SweepRawHistory(ctx context.Context, now time.Time) (RawHistorySweep, error) {
	return d.sweepRawHistory(ctx, now, 0)
}

func (d *DB) sweepRawHistory(ctx context.Context, now time.Time, maxBatches int) (RawHistorySweep, error) {
	var total RawHistorySweep
	d.rawSweepMu.Lock()
	defer d.rawSweepMu.Unlock()
	var p rawSweepProgress
	err := d.reader().QueryRowContext(ctx, `SELECT cutoff,after_row,last_row,next_run FROM raw_sweep_progress WHERE id=1`).Scan(&p.cutoff, &p.after, &p.last, &p.nextRun)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return total, err
	}
	if p.nextRun > now.UnixMilli() {
		return total, nil
	}
	if p.cutoff == 0 || p.nextRun != 0 {
		p = rawSweepProgress{cutoff: rawHistoryCutoff(now)}
		if err := d.reader().QueryRowContext(ctx, `SELECT COALESCE(max(rowid),0) FROM message`).Scan(&p.last); err != nil {
			return total, err
		}
	}
	total.Pending = true
	started := time.Now()
	for batch := 0; time.Since(started) < rawSweepBudget && (maxBatches == 0 || batch < maxBatches); batch++ {
		stats := d.db.Stats()
		if stats.InUse != 0 {
			return total, nil // give the writer to ingestion already in flight
		}
		batchStart := time.Now()
		page, err := d.rawSweepPage(ctx, &p, now)
		if err != nil {
			return total, err
		}
		total.Messages += page.Messages
		total.Snapshots += page.Snapshots
		total.Bytes += page.Bytes
		if p.nextRun != 0 {
			total.Pending = false
			return total, nil
		}
		if d.db.Stats().WaitCount > stats.WaitCount {
			return total, nil // foreground work queued while this batch ran
		}
		pause := min(max(10*time.Millisecond, 4*time.Since(batchStart)), time.Until(started.Add(rawSweepBudget)))
		if pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return total, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return total, ctx.Err()
}

func (d *DB) rawSweepPage(ctx context.Context, p *rawSweepProgress, now time.Time) (RawHistorySweep, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return RawHistorySweep{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var result RawHistorySweep
	next := *p
	if p.after < p.last {
		result, err = sweepRawMessages(ctx, tx, &next)
	} else {
		result, err = sweepRawArchives(ctx, tx, p.cutoff)
		if result.Snapshots == 0 {
			next.nextRun = now.Add(24 * time.Hour).UnixMilli()
		}
	}
	if err != nil {
		return RawHistorySweep{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO raw_sweep_progress(id,cutoff,after_row,last_row,next_run)
			VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET cutoff=excluded.cutoff,after_row=excluded.after_row,
			last_row=excluded.last_row,next_run=excluded.next_run`, next.cutoff, next.after, next.last, next.nextRun)
	if err != nil {
		return RawHistorySweep{}, err
	}
	if err := tx.Commit(); err != nil {
		return RawHistorySweep{}, err
	}
	*p = next
	return result, nil
}

func sweepRawMessages(ctx context.Context, tx *sql.Tx, p *rawSweepProgress) (RawHistorySweep, error) {
	const byteBudget = 2 << 20
	var result RawHistorySweep
	through := min(p.last, p.after+256)
	rows, err := tx.QueryContext(ctx, `SELECT rowid,extensions,created_at FROM message
		WHERE rowid>? AND rowid<=? AND created_at<? ORDER BY rowid`, p.after, through, p.cutoff)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	type update struct {
		id  int64
		ext []byte
	}
	var updates []update
	var bytesRead int
	var stopped bool
	for rows.Next() {
		var id, createdAt int64
		var ext sql.RawBytes
		if err := rows.Scan(&id, &ext, &createdAt); err != nil {
			return result, err
		}
		if bytesRead > 0 && bytesRead+len(ext) > byteBudget {
			stopped = true
			break
		}
		bytesRead += len(ext)
		p.after = id
		if compacted := expireRawHistory(json.RawMessage(ext), createdAt, p.cutoff); compacted != nil {
			result.Messages++
			result.Bytes += int64(len(ext) - len(compacted))
			updates = append(updates, update{id, compacted})
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	if !stopped {
		p.after = through
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE message SET extensions=? WHERE rowid=?`, string(u.ext), u.id); err != nil {
			return result, err
		}
	}
	return result, nil
}
