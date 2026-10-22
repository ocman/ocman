package state

import (
	"database/sql"
	"fmt"
)

// v100 adds viewer-scoped preview-provider consent (preview_auth.go).
// v101 adds artifacts and their relay shares (artifacts.go).
// v102 adds viewer-scoped Inbox item pins.
// v113 indexes running routine runs in scheduler polling order.
const latestSchemaVersion = 125

// migrations maps each schema version to its DDL. Index 0 is unused.
var migrations = []func(*sql.Tx) error{
	1:   migrateToV1,
	2:   migrateToV2,
	3:   migrateToV3,
	4:   migrateToV4,
	5:   migrateToV5,
	6:   migrateToV6,
	7:   migrateToV7,
	8:   migrateToV8,
	9:   migrateToV9,
	10:  migrateToV10,
	11:  migrateToV11,
	12:  migrateToV12,
	13:  migrateToV13,
	14:  migrateToV14,
	15:  migrateToV15,
	16:  migrateToV16,
	17:  migrateToV17,
	18:  migrateToV18,
	19:  migrateToV19,
	20:  migrateToV20,
	21:  migrateToV21,
	22:  migrateToV22,
	23:  migrateToV23,
	24:  migrateToV24,
	25:  migrateToV25,
	26:  migrateToV26,
	27:  migrateToV27,
	28:  migrateToV28,
	29:  migrateToV29,
	30:  migrateToV30,
	31:  migrateToV31,
	32:  migrateToV32,
	33:  migrateToV33,
	34:  migrateToV34,
	35:  migrateToV35,
	36:  migrateToV36,
	37:  migrateToV37,
	38:  migrateToV38,
	39:  migrateToV39,
	40:  migrateToV40,
	41:  migrateToV41,
	42:  migrateToV42,
	43:  migrateToV43,
	44:  migrateToV44,
	45:  migrateToV45,
	46:  migrateToV46,
	47:  migrateToV47,
	48:  migrateToV48,
	49:  migrateToV49,
	50:  migrateToV50,
	51:  migrateToV51,
	52:  migrateToV52,
	53:  migrateToV53,
	54:  migrateToV54,
	55:  migrateToV55,
	56:  migrateToV56,
	57:  migrateToV57,
	58:  migrateToV58,
	59:  migrateToV59,
	60:  migrateToV60,
	61:  migrateToV61,
	62:  migrateToV62,
	63:  migrateToV63,
	64:  migrateToV64,
	65:  migrateToV65,
	66:  migrateToV66,
	67:  migrateToV67,
	68:  migrateToV68,
	69:  migrateToV69,
	70:  migrateToV70,
	71:  migrateToV71,
	72:  migrateToV72,
	73:  migrateToV73,
	74:  migrateToV74,
	75:  migrateToV75,
	76:  migrateToV76,
	77:  migrateToV77,
	78:  migrateToV78,
	79:  migrateToV79,
	80:  migrateToV80,
	81:  migrateToV81,
	82:  migrateToV82,
	83:  migrateToV83,
	84:  migrateToV84,
	85:  migrateToV85,
	86:  migrateToV86,
	87:  migrateToV87,
	88:  migrateToV88,
	89:  migrateToV89,
	90:  migrateToV90,
	91:  migrateToV91,
	92:  migrateToV92,
	93:  migrateToV93,
	94:  migrateToV94,
	95:  migrateToV95,
	96:  migrateToV96,
	97:  migrateToV97,
	98:  migrateToV98,
	99:  migrateToV99,
	100: migrateToV100,
	101: migrateToV101,
	102: migrateToV102,
	103: migrateToV103,
	104: migrateToV104,
	105: migrateToV105,
	106: migrateToV106,
	107: migrateToV107,
	108: migrateToV108,
	109: migrateToV109,
	110: migrateToV110,
	111: migrateToV102,
	112: migrateSeenSessionInterrupted,
	113: migrateRoutineRunRunningIndex,
	114: migrateToV114,
	115: migrateToV115,
	116: migrateToV116,
	117: migrateToV117,
	118: migrateToV118,
	119: migrateToV119,
	120: migrateUIUsage,
	121: migrateRoutineWorktrees,
	122: migrateToV122,
	123: migrateSnooze,
	124: migrateHistoryDatabaseSize,
	125: migrateConversationProjectCache,
}

// applyMigration runs the DDL for the given target version.
func applyMigration(tx *sql.Tx, target int) error {
	if target < 1 || target >= len(migrations) {
		return fmt.Errorf("no migration registered for v%d", target)
	}
	return migrations[target](tx)
}

func migrateToV94(tx *sql.Tx) error {
	if err := migrateToV74(tx); err != nil {
		return err
	}
	if err := addColumnIfMissing(tx, "inbox_item", "category", "TEXT NOT NULL DEFAULT 'general'"); err != nil {
		return err
	}
	return addColumnIfMissing(tx, "inbox_item", "permission_json", "TEXT NOT NULL DEFAULT ''")
}

func migrateToV95(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS factory_workflow_step (
		issue_id TEXT PRIMARY KEY REFERENCES factory_issue(id),
		definition_json TEXT NOT NULL
	)`)
	return err
}

func migrateToV98(tx *sql.Tx) error {
	return addColumnIfMissing(tx, "inbox_item", "session_json", "TEXT NOT NULL DEFAULT ''")
}

func migrateToV99(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='factory_epic')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "factory_epic", "models_json", "TEXT NOT NULL DEFAULT '{}'")
}

func migrateToV102(tx *sql.Tx) error {
	// v111 repeats v102 for databases that took v102 from the webhook-inbox
	// branch before the pin migration claimed that slot.
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pinned_inbox_item (
		remote_id TEXT NOT NULL,
		item_id TEXT NOT NULL,
		pinned_at INTEGER NOT NULL,
		PRIMARY KEY (remote_id, item_id)
	)`)
	return err
}

func migrateToV103(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_delivery')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "webhook_delivery", "headers_json", "TEXT NOT NULL DEFAULT '{}'")
}

func migrateToV104(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_inbox')`).Scan(&exists); err != nil || !exists {
		return err
	}
	if err := addColumnIfMissing(tx, "webhook_inbox", "name", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='routine')`).Scan(&exists); err != nil || !exists {
		return err
	}
	_, err := tx.Exec(`UPDATE webhook_inbox SET name = COALESCE((SELECT r.name FROM routine r WHERE r.id = webhook_inbox.routine_id), '') WHERE name = ''`)
	return err
}

func migrateToV105(tx *sql.Tx) error {
	// Databases hit by the v77 schema-version collision got webhook_inbox
	// back from v79-v81 but never webhook_delivery.
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS webhook_delivery (
		inbox_id TEXT NOT NULL REFERENCES webhook_inbox(id) ON DELETE CASCADE,
		delivery_id TEXT NOT NULL,
		item_id TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		next_retry_at INTEGER NOT NULL DEFAULT 0,
		accepted_at INTEGER NOT NULL,
		headers_json TEXT NOT NULL DEFAULT '{}',
		PRIMARY KEY (inbox_id, delivery_id)
	)`)
	return err
}

func migrateToV106(tx *sql.Tx) error {
	// Empty means no shared secret, or an inbox registered before ocman
	// recorded the header.
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_inbox')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "webhook_inbox", "secret_header", "TEXT NOT NULL DEFAULT ''")
}

func migrateToV107(tx *sql.Tx) error {
	// The delivery log keeps its own request title and body; deliveries no
	// longer post Inbox items, and the ones they did post move out.
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_delivery')`).Scan(&exists); err != nil || !exists {
		return err
	}
	for _, column := range []string{"title", "body"} {
		if err := addColumnIfMissing(tx, "webhook_delivery", column, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE webhook_delivery SET
			title = COALESCE((SELECT i.title FROM inbox_item i WHERE i.id = webhook_delivery.item_id), ''),
			body = COALESCE((SELECT i.body FROM inbox_item i WHERE i.id = webhook_delivery.item_id), '')
		WHERE item_id != '';
		DELETE FROM inbox_item WHERE id IN (SELECT item_id FROM webhook_delivery WHERE item_id != '')`)
	return err
}

func migrateToV108(tx *sql.Tx) error {
	// Routines report only runs that don't succeed unless they opt in; the
	// run keeps the choice in force when it started, like its other options.
	for _, table := range []string{"routine", "routine_run"} {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := addColumnIfMissing(tx, table, "notify_on_success", "INTEGER NOT NULL DEFAULT 0 CHECK (notify_on_success IN (0, 1))"); err != nil {
			return err
		}
	}
	return nil
}

func migrateToV109(tx *sql.Tx) error {
	// Keep the inbox shared secret so the UI can show it back. Older inboxes
	// stay empty: only the relay knows their secret, as a hash.
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_inbox')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "webhook_inbox", "secret", "TEXT NOT NULL DEFAULT ''")
}

func migrateToV110(tx *sql.Tx) error {
	// Keep the request query too, so a redelivery replays it: the routine
	// prompt carries it. Deliveries logged earlier replay without one.
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_delivery')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "webhook_delivery", "query_json", "TEXT NOT NULL DEFAULT ''")
}

func migrateSeenSessionInterrupted(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='seen_session')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "seen_session", "interrupted", "INTEGER NOT NULL DEFAULT 0")
}

func migrateRoutineRunRunningIndex(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='routine_run')`).Scan(&exists); err != nil || !exists {
		return err
	}
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS routine_run_running_idx
		ON routine_run (created_at, id) WHERE state = 'running'`)
	return err
}

func migrateToV114(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_interruption (
		platform TEXT NOT NULL,
		session_id TEXT NOT NULL,
		message_id TEXT NOT NULL,
		observed_at INTEGER NOT NULL,
		message TEXT NOT NULL,
		PRIMARY KEY (platform, session_id, message_id)
	)`)
	return err
}

func migrateToV115(tx *sql.Tx) error {
	if err := addColumnIfMissing(tx, "session_interruption", "replacement_root", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return addColumnIfMissing(tx, "session_interruption", "confirmed", "INTEGER NOT NULL DEFAULT 1 CHECK (confirmed IN (0, 1))")
}

func migrateToV117(tx *sql.Tx) error {
	if err := addColumnIfMissing(tx, "session_replacement", "stop_started_at", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return addColumnIfMissing(tx, "session_replacement", "stop_baseline_json", "TEXT NOT NULL DEFAULT '{}'")
}

func migrateToV118(tx *sql.Tx) error {
	return addColumnIfMissing(tx, "session_replacement", "stop_runtime_json", "TEXT NOT NULL DEFAULT ''")
}

func migrateToV122(tx *sql.Tx) error {
	// Main and the provider branch independently used v112/v113.
	// Replay main's idempotent steps for an existing provider database.
	if err := migrateSeenSessionInterrupted(tx); err != nil {
		return err
	}
	if err := migrateRoutineRunRunningIndex(tx); err != nil {
		return err
	}
	if err := migrateToV112(tx); err != nil {
		return err
	}
	return migrateToV113(tx)
}
