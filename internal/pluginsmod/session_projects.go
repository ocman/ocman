package pluginsmod

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/ocman/ocman/internal/db"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func migratePluginSessionProjects(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE plugin_session_project (
		directory TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, project_directory TEXT NOT NULL
	)`)
	return err
}

// A prepared directory has one durable display owner, even after its files vanish.
// Reusing it for a different project would misattribute earlier sessions.
func (d *Store) associateSessionProject(ctx context.Context, pluginID, directory, project string) error {
	if !filepath.IsAbs(directory) || !filepath.IsAbs(project) || directory == project {
		return plugins.ErrInvalidMessage
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_session_project (directory,plugin_id,project_directory)
		VALUES (?,?,?) ON CONFLICT(directory) DO UPDATE SET project_directory=excluded.project_directory
		WHERE plugin_session_project.plugin_id=excluded.plugin_id AND plugin_session_project.project_directory=excluded.project_directory`, directory, pluginID, project)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return &plugins.WireError{Category: plugins.ErrorConflict}
	}
	return nil
}

func (d *Store) sessionProjectAssociations(ctx context.Context) (map[string]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT directory,project_directory FROM plugin_session_project`)
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
