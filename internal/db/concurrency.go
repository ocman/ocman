package db

import (
	"context"
	"sort"
	"time"
)

type ConcurrencyPoint struct {
	Timestamp int64 `json:"timestamp"`
	Sessions  int   `json:"sessions"`
}

type SessionConcurrency struct {
	BucketMs int64              `json:"bucketMs"`
	Series   []ConcurrencyPoint `json:"series"`
}

type concurrencyEvent struct {
	at    int64
	delta int
}

// GetSessionConcurrency reconstructs historical concurrency from completed
// assistant timings. Unfinished messages cannot establish a historical end.
// Overlapping messages in the same session count once; subagents count separately.
func (d *DB) GetSessionConcurrency(ctx context.Context, since, until int64, dir string) (SessionConcurrency, error) {
	result := SessionConcurrency{Series: []ConcurrencyPoint{}}
	query := `SELECT m.session_id, json_extract(m.data, '$.time.created'), json_extract(m.data, '$.time.completed')
		FROM message m JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		AND json_extract(m.data, '$.time.created') > 0
		AND json_extract(m.data, '$.time.completed') > json_extract(m.data, '$.time.created')
		AND json_extract(m.data, '$.time.completed') > ?
		AND json_extract(m.data, '$.time.created') < ?`
	args := []any{since, until}
	if clause, values := directoryWhere(dir); clause != "" {
		query += " AND " + clause
		args = append(args, values...)
	}
	query += ` ORDER BY m.session_id, 2, 3`
	rows, err := d.analytics(ctx).QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	var events []concurrencyEvent
	var session string
	var from, to int64
	appendInterval := func() {
		if to > from {
			events = append(events, concurrencyEvent{from, 1}, concurrencyEvent{to, -1})
		}
	}
	for rows.Next() {
		var id string
		var start, end int64
		if err := rows.Scan(&id, &start, &end); err != nil {
			return result, err
		}
		start, end = max(start, since), min(end, until)
		if id == session && start <= to {
			to = max(to, end)
		} else {
			appendInterval()
			session, from, to = id, start, end
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	appendInterval()
	if since <= 0 {
		if len(events) == 0 {
			return result, nil
		}
		since = events[0].at
		for _, event := range events {
			since = min(since, event.at)
		}
	}
	if until <= since {
		return result, nil
	}
	// Keep the response bounded; hourly buckets widen for longer ranges.
	hour := time.Hour.Milliseconds()
	result.BucketMs = max(hour, ((until-since+1000*hour-1)/(1000*hour))*hour)
	result.Series = concurrencyBuckets(events, since, until, result.BucketMs)
	return result, nil
}

func concurrencyBuckets(events []concurrencyEvent, since, until, width int64) []ConcurrencyPoint {
	sort.Slice(events, func(i, j int) bool { return events[i].at < events[j].at })
	points := []ConcurrencyPoint{}
	index, active := 0, 0
	for start := since; start < until; start += width {
		end := min(start+width, until)
		peak := active
		for index < len(events) && events[index].at < end {
			at, delta := events[index].at, 0
			for index < len(events) && events[index].at == at {
				delta += events[index].delta
				index++
			}
			active += delta
			// Ends at a bucket boundary belong only to the preceding bucket.
			if at == start {
				peak = active
			} else {
				peak = max(peak, active)
			}
		}
		points = append(points, ConcurrencyPoint{start, peak})
	}
	return points
}
