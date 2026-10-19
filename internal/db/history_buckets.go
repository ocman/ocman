package db

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"
)

type historyBucket struct {
	label, provider, model     string
	messages                   int
	input, output, read, write int64
	first                      int64
}

// historyBuckets combines full UTC-hour summaries with exact boundary facts.
// Local calendar buckets that split a UTC hour (e.g. Asia/Kathmandu) also read
// only their boundary facts. No persisted timezone or rounded cutoff is used.
func (d *DB) historyBuckets(ctx context.Context, since int64, role, dir, format string, valid bool) ([]historyBucket, error) {
	const hour = int64(3600000)
	since = max(0, since)
	floor := since - since%hour
	edge := floor
	if since%hour != 0 {
		if floor <= math.MaxInt64-hour {
			edge += hour
		} else {
			edge = math.MaxInt64
		}
	}
	filters := ""
	if valid {
		filters += ` AND h.valid=1`
	}
	if role == "user" {
		filters += ` AND s.is_subagent=0`
	}
	dirClause, dirArgs := directoryWhere(dir)
	if dirClause != "" {
		filters += ` AND ` + dirClause
	}
	query := `SELECT h.bucket,h.provider,h.model,sum(h.messages),sum(h.input_tokens),sum(h.output_tokens),sum(h.cache_read),sum(h.cache_write),min(h.first_at)
		FROM history_hour h JOIN session s ON s.instance_id=h.instance_id AND s.id=h.session_id
		WHERE h.role=? AND h.bucket>=?` + filters + ` GROUP BY h.bucket,h.provider,h.model`
	args := []any{role, floor}
	args = append(args, dirArgs...)
	rows, err := d.reads().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ label, provider, model string }
	groups := map[key]historyBucket{}
	add := func(b historyBucket) {
		k := key{b.label, b.provider, b.model}
		a, ok := groups[k]
		if !ok {
			groups[k] = b
			return
		}
		a.messages += b.messages
		a.input += b.input
		a.output += b.output
		a.read += b.read
		a.write += b.write
		a.first = min(a.first, b.first)
		groups[k] = a
	}
	boundaries := map[int64]bool{}
	// Label distinct UTC buckets in Go, not once per session/message in SQLite.
	// A UTC hour may split a local day/hour; those buckets use exact facts below.
	for rows.Next() {
		var b historyBucket
		var bucket int64
		if err := rows.Scan(&bucket, &b.provider, &b.model, &b.messages, &b.input, &b.output, &b.read, &b.write, &b.first); err != nil {
			return nil, err
		}
		b.label = historyCalendarLabel(bucket, format)
		if bucket < edge || b.label != historyCalendarLabel(bucket+hour-1, format) {
			boundaries[bucket] = true
			continue
		}
		add(b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(boundaries) > 0 {
		buckets := make([]int64, 0, len(boundaries))
		for bucket := range boundaries {
			buckets = append(buckets, bucket)
		}
		raw, _ := json.Marshal(buckets)
		query = `SELECT f.time_created,f.provider,f.model,f.input_tokens,f.output_tokens,f.cache_read,f.cache_write
			FROM json_each(?) boundary CROSS JOIN history_fact f JOIN session s ON s.instance_id=f.instance_id AND s.id=f.session_id
			WHERE f.role=? AND f.time_created>=max(?,boundary.value) AND f.time_created<boundary.value+3600000`
		if valid {
			query += ` AND f.valid=1`
		}
		if role == "user" {
			query += ` AND s.is_subagent=0`
		}
		if dirClause != "" {
			query += ` AND ` + dirClause
		}
		args = []any{string(raw), role, since}
		args = append(args, dirArgs...)
		rows, err = d.reads().QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var b historyBucket
			b.messages = 1
			if err := rows.Scan(&b.first, &b.provider, &b.model, &b.input, &b.output, &b.read, &b.write); err != nil {
				return nil, err
			}
			b.label = historyCalendarLabel(b.first, format)
			add(b)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	result := make([]historyBucket, 0, len(groups))
	for _, b := range groups {
		result = append(result, b)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.label != b.label {
			return a.label < b.label
		}
		if a.first != b.first {
			return a.first < b.first
		}
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		return a.model < b.model
	})
	return result, nil
}

func historyCalendarLabel(at int64, format string) string {
	if format == "" {
		return ""
	}
	layout := "2006-01-02"
	if format == "%Y-%m-%d %H" {
		layout += " 15"
	}
	return time.UnixMilli(at).Local().Format(layout)
}
