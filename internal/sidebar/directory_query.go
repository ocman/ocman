package sidebar

import "strings"

// A known directory must be an equality constraint for SQLite to seek directly
// into sidebar_directory_recency. Retain both bound directory parameters and
// every owner, publication, visibility and pagination predicate unchanged.
var selectDirectorySessions = strings.Replace(selectSessions,
	"(?='' OR s.directory=?)", "(s.directory=? AND ?<>'')", 1)
