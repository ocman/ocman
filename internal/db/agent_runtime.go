package db

import (
	"context"
	"sort"
	"time"
)

type RunInterval struct{ Start, End int64 }

type AgentRunHour struct {
	Timestamp int64   `json:"timestamp"`
	Minutes   float64 `json:"minutes"`
}

// GetAgentRunHours unions completed assistant intervals per session, including
// tools, then subtracts the union of human waits. Subagents remain separate.
func (d *DB) GetAgentRunHours(ctx context.Context, since, until int64, dir string, waits map[string][]RunInterval) ([]AgentRunHour, error) {
	source := d.analytics(ctx)
	query := `SELECT m.session_id, json_extract(m.data, '$.time.created'), json_extract(m.data, '$.time.completed')
		FROM message m JOIN session s ON s.id = m.session_id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		AND json_extract(m.data, '$.time.created') > 0
		AND json_extract(m.data, '$.time.completed') > json_extract(m.data, '$.time.created')
		AND json_extract(m.data, '$.time.completed') > ? AND json_extract(m.data, '$.time.created') < ?`
	args := []any{since, until}
	if clause, values := directoryWhere(dir); clause != "" {
		query += " AND " + clause
		args = append(args, values...)
	}
	rows, err := source.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	runs := make(map[string][]RunInterval)
	for rows.Next() {
		var session string
		var interval RunInterval
		if err := rows.Scan(&session, &interval.Start, &interval.End); err != nil {
			rows.Close()
			return nil, err
		}
		runs[session] = append(runs[session], interval)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Read only compact question timings from the same source as the message
	// scan. Before the mirror is warm, the equivalent source projection works.
	questionTable, questionTime, questionTool := "part", "json_extract(p.data, '$.state.time')", "json_extract(p.data, '$.tool')"
	if source != d.db {
		questionTable, questionTime, questionTool = "tool_timing", "p.time", "p.tool"
	}
	questionQuery := `SELECT m.session_id, json_extract(` + questionTime + `, '$.start'), json_extract(` + questionTime + `, '$.end')
		FROM ` + questionTable + ` p JOIN message m ON m.id = p.message_id JOIN session s ON s.id = m.session_id
		WHERE ` + questionTool + ` = 'question' AND json_extract(m.data, '$.time.completed') > ?
		AND json_extract(m.data, '$.time.created') < ?`
	if clause, _ := directoryWhere(dir); clause != "" {
		questionQuery += " AND " + clause
	}
	rows, err = source.QueryContext(ctx, questionQuery, args...)
	if err != nil {
		return nil, err
	}
	// A successful timing snapshot reconciles missed reply/idle events: a human
	// wait can block only the assistant interval in which it originated. Bind
	// before clipping to the requested range, so old waits cannot spill into a
	// later turn after restart. Failed snapshot reads return errors above.
	blocked := boundRunWaits(runs, waits)
	for rows.Next() {
		var session string
		var start, end *int64
		if err := rows.Scan(&session, &start, &end); err != nil {
			rows.Close()
			return nil, err
		}
		if start != nil && end != nil && *end > *start {
			blocked[session] = append(blocked[session], RunInterval{*start, *end})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for session, intervals := range runs {
		for i := range intervals {
			intervals[i].Start = max(since, intervals[i].Start)
			intervals[i].End = min(until, intervals[i].End)
		}
		runs[session] = intervals
	}
	return agentRunBuckets(runs, blocked, since, until), nil
}

func boundRunWaits(runs, waits map[string][]RunInterval) map[string][]RunInterval {
	bounded := make(map[string][]RunInterval, len(waits))
	for session, intervals := range waits {
		owners := runs[session]
		sort.Slice(owners, func(i, j int) bool { return owners[i].Start < owners[j].Start })
		for _, wait := range intervals {
			index := sort.Search(len(owners), func(i int) bool { return owners[i].Start > wait.Start }) - 1
			for ; index >= 0; index-- {
				owner := owners[index]
				if wait.Start < owner.End {
					bounded[session] = append(bounded[session], RunInterval{wait.Start, min(wait.End, owner.End)})
					break
				}
			}
		}
	}
	return bounded
}

func unionRunIntervals(intervals []RunInterval) []RunInterval {
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].Start < intervals[j].Start })
	merged := []RunInterval{}
	for _, interval := range intervals {
		if interval.End <= interval.Start {
			continue
		}
		if len(merged) > 0 && interval.Start <= merged[len(merged)-1].End {
			merged[len(merged)-1].End = max(merged[len(merged)-1].End, interval.End)
		} else {
			merged = append(merged, interval)
		}
	}
	return merged
}

func agentRunBuckets(runs, waits map[string][]RunInterval, since, until int64) []AgentRunHour {
	hour := time.Hour.Milliseconds()
	totals := make(map[int64]int64)
	add := func(start, end int64) {
		for start < end {
			bucket := start / hour * hour
			to := min(end, bucket+hour)
			totals[bucket] += to - start
			start = to
		}
	}
	for session, intervals := range runs {
		blocked := unionRunIntervals(waits[session])
		index := 0
		for _, run := range unionRunIntervals(intervals) {
			if since <= 0 {
				since = run.Start
			} else {
				since = min(since, run.Start)
			}
			cursor := run.Start
			for index < len(blocked) && blocked[index].End <= cursor {
				index++
			}
			for j := index; j < len(blocked) && blocked[j].Start < run.End; j++ {
				add(cursor, min(run.End, blocked[j].Start))
				cursor = max(cursor, blocked[j].End)
			}
			add(cursor, run.End)
		}
	}
	result := []AgentRunHour{}
	if since <= 0 {
		return result
	}
	for at := since / hour * hour; at < until; at += hour {
		result = append(result, AgentRunHour{at, float64(totals[at]) / 60_000})
	}
	return result
}
