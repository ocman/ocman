package platformconnections

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrConfigurationOwned includes removed connections: retained history must
// never acquire a different account identity through remove/re-add.
var ErrConfigurationOwned = fmt.Errorf("%w: native configuration is already owned by another connection", ErrInvalid)

func canonicalConfiguration(kind, directory string) (string, error) {
	if directory == "" || (kind != "claudecode" && kind != "codex") {
		return directory, nil
	}
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("%w: native configuration must be an absolute path", ErrInvalid)
	}
	// Isolated configuration directories need not exist yet. Resolve the
	// existing ancestor too, so aliases of their future physical path collide.
	path := filepath.Clean(directory)
	var missing []string
	for {
		canonical, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				canonical = filepath.Join(canonical, missing[i])
			}
			return canonical, nil
		}
		if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(path) == path {
			return "", fmt.Errorf("%w: resolve native configuration: %w", ErrInvalid, err)
		}
		if _, statErr := os.Lstat(path); !errors.Is(statErr, fs.ErrNotExist) {
			// A dangling symlink has no canonical physical identity yet.
			return "", fmt.Errorf("%w: native configuration has an unresolved path %q", ErrInvalid, path)
		}
		missing = append(missing, filepath.Base(path))
		path = filepath.Dir(path)
	}
}

func configurationWriteError(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: platform_connection.kind, platform_connection.native_directory") {
		return ErrConfigurationOwned
	}
	return err
}

func migrateNativeConfigurationOwnership(tx *sql.Tx) error {
	// Keep the owner-pinned spelling of directory intact. Host containment and
	// retry receipts use it; native_directory is only physical ownership identity.
	if _, err := tx.Exec(`ALTER TABLE platform_connection ADD COLUMN native_directory TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id,kind,directory FROM platform_connection WHERE kind IN ('claudecode','codex') AND directory<>'' ORDER BY id`)
	if err != nil {
		return err
	}
	type configuration struct{ id, kind, directory string }
	var configurations []configuration
	owners := map[string]string{}
	for rows.Next() {
		var c configuration
		if err := rows.Scan(&c.id, &c.kind, &c.directory); err != nil {
			_ = rows.Close()
			return err
		}
		c.directory, err = canonicalConfiguration(c.kind, c.directory)
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("connection %s: %w", c.id, err)
		}
		key := c.kind + "\x00" + c.directory
		if previous := owners[key]; previous != "" {
			_ = rows.Close()
			return fmt.Errorf("%w: connections %s and %s share %s configuration %q; reconcile ownership before startup", ErrConfigurationOwned, previous, c.id, c.kind, c.directory)
		}
		owners[key] = c.id
		configurations = append(configurations, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, c := range configurations {
		if _, err := tx.Exec(`UPDATE platform_connection SET native_directory=? WHERE id=?`, c.directory, c.id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`CREATE UNIQUE INDEX platform_connection_native_directory ON platform_connection(kind,native_directory)
		WHERE kind IN ('claudecode','codex') AND native_directory<>'';
		CREATE TRIGGER platform_connection_native_identity_required BEFORE INSERT ON platform_connection
		WHEN NEW.kind IN ('claudecode','codex') AND NEW.directory<>'' AND NEW.native_directory=''
		BEGIN SELECT RAISE(ABORT,'native configuration physical identity is required'); END;`)
	return err
}
