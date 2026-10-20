package db

import "context"

// Historical rows carry metadata from the same validated read snapshot. Once
// SessionID is projected to an Ocman identity, looking it up as a native DB ID
// would lose the title/directory or accidentally use another source's row.
func (d *DB) requestSessionMetadata(ctx context.Context, rows []requestRow) (map[string]string, map[string]string, error) {
	titles, directories := map[string]string{}, map[string]string{}
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if row.historyMetadata {
			titles[row.SessionID], directories[row.SessionID] = row.sessionTitle, row.sessionDirectory
		} else if !seen[row.SessionID] {
			seen[row.SessionID] = true
			ids = append(ids, row.SessionID)
		}
	}
	if len(ids) == 0 {
		return titles, directories, nil
	}
	legacyTitles, legacyDirectories, err := d.lookupSessionMetadata(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	for id, title := range legacyTitles {
		titles[id] = title
		directories[id] = legacyDirectories[id]
	}
	return titles, directories, nil
}
