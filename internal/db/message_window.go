package db

import "time"

// indexedWindowMaxAge is the oldest `since` for which reading messages
// through message_session_time_created_id_idx beats a full table scan.
// Measured on a 253k-message database: 7 days 6.4s -> 0.7s, 30 days
// 7.4s -> 3.1s, 90 days 7.3s -> 8.2s (random reads overtake the scan).
const indexedWindowMaxAge = 31 * 24 * time.Hour

// messagesFrom returns the FROM clause for a query over messages created at
// or after since (unix ms, <=0 = all time). The session table is always
// available as `s` when joinSession is true or the window is indexed.
//
// OpenCode's DB has no ANALYZE stats, so the planner always scans `message`,
// reading every row's (attachment-bloated) data. For a recent window the
// CROSS JOIN pins session as the outer loop, turning that scan into one
// (session_id, time_created>=?) index range per session. Every message has
// a session row (ON DELETE CASCADE foreign key), so no rows are lost.
func messagesFrom(since int64, joinSession bool) string {
	if since > 0 && time.Since(time.UnixMilli(since)) <= indexedWindowMaxAge {
		return "session s CROSS JOIN message m ON m.session_id = s.id"
	}
	if joinSession {
		return "message m JOIN session s ON s.id = m.session_id"
	}
	return "message m"
}
