package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/server"
)

// openOpenCodeDB opens the OpenCode database. A missing file is not
// fatal: it returns a nil database and a startup issue so ocman runs as
// if opencode were omitted from -platforms. Any other error is returned.
func openOpenCodeDB(path string) (*db.DB, *server.StartupIssue, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, &server.StartupIssue{
			ID:      "opencode-db-missing",
			Message: fmt.Sprintf("OpenCode database not found at %s", path),
			Hint:    "Install OpenCode and run it at least once, then restart ocman.",
		}, nil
	}
	database, err := db.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}
	return database, nil, nil
}
