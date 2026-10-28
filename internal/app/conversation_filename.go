package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/startup"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

// Adoption is remembered in application state, not in the conversation file:
// explicitly removing state_conv.db later must rebuild, not resurrect the old
// statev2.db. Custom and legacy explicit paths keep their existing behaviour.
func openApplicationHistory(ctx context.Context, core *state.DB, statePath, override string, getenv func(string) string) (*statev2.DB, error) {
	path, err := filepath.Abs(conversationHistoryPath(statePath, override, getenv))
	if err != nil {
		return nil, err
	}
	var marker string
	if filepath.Base(path) == "state_conv.db" {
		marker = fmt.Sprintf("conversation-filename/%x", sha256.Sum256([]byte(path)))
		_, adopted, err := core.GetSetting(ctx, marker)
		if err != nil {
			return nil, err
		}
		if !adopted {
			if err := adoptLegacyConversationFile(ctx, path); err != nil {
				return nil, err
			}
		}
	}
	store, err := openConversationHistory(statePath, path, getenv)
	if err != nil {
		return nil, err
	}
	if marker != "" {
		if err := core.SetSetting(ctx, marker, "adopted"); err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	return store, nil
}

// Copy with SQLite, not filesystem copy/rename: uncheckpointed WAL data must
// survive and another process may still have the old filename open. Publish
// without replacing an existing destination. The original is retained.
func adoptLegacyConversationFile(ctx context.Context, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	source := filepath.Join(filepath.Dir(target), "statev2.db")
	development := filepath.Join(filepath.Dir(target), "tmp", "state_conv.db")
	if _, err := os.Lstat(development); err == nil {
		source = development
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, path := range []string{filepath.Dir(target), filepath.Dir(source), source, source + "-wal", source + "-shm"} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return fmt.Errorf("conversation adoption requires regular files and directories")
		}
	}
	startup.Report(ctx, "Preserving the existing conversation database", 0, 0)
	u := url.URL{Scheme: "file", Path: source, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var appID int
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return err
	}
	if appID != 0x4f437632 {
		return fmt.Errorf("refusing to adopt a foreign conversation database")
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".conversation-adoption-*.db")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp.Name()); err != nil {
		return fmt.Errorf("preserving legacy conversations: %w", err)
	}
	if err := os.Link(tmp.Name(), target); err != nil {
		return fmt.Errorf("publishing conversation database without overwriting: %w", err)
	}
	log.WithField("source", source).Info("preserved legacy conversations as state_conv.db; original retained")
	return nil
}
