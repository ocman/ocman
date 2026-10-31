package pluginsmod

import (
	"context"
	"database/sql"

	"github.com/ocman/ocman/internal/db"
)

func migratePluginSessionProjects(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE plugin_session_project (
		directory TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, project_directory TEXT NOT NULL
	)`)
	return err
}

func (d *Store) sessionProjectAssociations(ctx context.Context) (map[string]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT directory,project_directory FROM plugin_session_project WHERE committed=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var directory, project string
		if err := rows.Scan(&directory, &project); err != nil {
			return nil, err
		}
		out[directory] = project
	}
	return out, rows.Err()
}

// AssociateProjects overlays display aliases on this owner's inventory. Keep the
// source row and all its statistics so session lookup still uses its real path.
// Include a zero-stat target when it has no history of its own.
func (m *Module) AssociateProjects(ctx context.Context, projects []db.ProjectStats) ([]db.ProjectStats, error) {
	if m.plugins == nil {
		return projects, nil
	}
	associations, err := m.plugins.sessionProjectAssociations(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(projects))
	for _, p := range projects {
		known[p.Directory] = true
	}
	for i := range projects {
		if target := associations[projects[i].Directory]; target != "" {
			projects[i].AssociatedDirectory = target
			if !known[target] {
				projects = append(projects, db.ProjectStats{Directory: target})
				known[target] = true
			}
		}
	}
	return projects, nil
}
