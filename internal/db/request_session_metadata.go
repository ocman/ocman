package db

import "context"

// Historical rows carry metadata from the same validated read snapshot. Once
// SessionID is projected to an Ocman identity, looking it up as a native DB ID
// would lose the title/directory or accidentally use another source's row.
func (d *DB) requestSessionMetadata(_ context.Context, rows []requestRow) (map[string]string, map[string]string, error) {
	titles, directories := map[string]string{}, map[string]string{}
	for _, row := range rows {
		titles[row.SessionID], directories[row.SessionID] = row.sessionTitle, row.sessionDirectory
	}
	return titles, directories, nil
}
