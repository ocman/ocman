package state

import (
	"database/sql"
	"fmt"
)

// v99 stores per-phase model choices on each Factory Epic.
const latestSchemaVersion = 99

// applyMigration runs the DDL for the given target version.
func applyMigration(tx *sql.Tx, target int) error {
	switch target {
	case 1:
		return migrateToV1(tx)
	case 2:
		return migrateToV2(tx)
	case 3:
		return migrateToV3(tx)
	case 4:
		return migrateToV4(tx)
	case 5:
		return migrateToV5(tx)
	case 6:
		return migrateToV6(tx)
	case 7:
		return migrateToV7(tx)
	case 8:
		return migrateToV8(tx)
	case 9:
		return migrateToV9(tx)
	case 10:
		return migrateToV10(tx)
	case 11:
		return migrateToV11(tx)
	case 12:
		return migrateToV12(tx)
	case 13:
		return migrateToV13(tx)
	case 14:
		return migrateToV14(tx)
	case 15:
		return migrateToV15(tx)
	case 16:
		return migrateToV16(tx)
	case 17:
		return migrateToV17(tx)
	case 18:
		return migrateToV18(tx)
	case 19:
		return migrateToV19(tx)
	case 20:
		return migrateToV20(tx)
	case 21:
		return migrateToV21(tx)
	case 22:
		return migrateToV22(tx)
	case 23:
		return migrateToV23(tx)
	case 24:
		return migrateToV24(tx)
	case 25:
		return migrateToV25(tx)
	case 26:
		return migrateToV26(tx)
	case 27:
		return migrateToV27(tx)
	case 28:
		return migrateToV28(tx)
	case 29:
		return migrateToV29(tx)
	case 30:
		return migrateToV30(tx)
	case 31:
		return migrateToV31(tx)
	case 32:
		return migrateToV32(tx)
	case 33:
		return migrateToV33(tx)
	case 34:
		return migrateToV34(tx)
	case 35:
		return migrateToV35(tx)
	case 36:
		return migrateToV36(tx)
	case 37:
		return migrateToV37(tx)
	case 38:
		return migrateToV38(tx)
	case 39:
		return migrateToV39(tx)
	case 40:
		return migrateToV40(tx)
	case 41:
		return migrateToV41(tx)
	case 42:
		return migrateToV42(tx)
	case 43:
		return migrateToV43(tx)
	case 44:
		return migrateToV44(tx)
	case 45:
		return migrateToV45(tx)
	case 46:
		return migrateToV46(tx)
	case 47:
		return migrateToV47(tx)
	case 48:
		return migrateToV48(tx)
	case 49:
		return migrateToV49(tx)
	case 50:
		return migrateToV50(tx)
	case 51:
		return migrateToV51(tx)
	case 52:
		return migrateToV52(tx)
	case 53:
		return migrateToV53(tx)
	case 54:
		return migrateToV54(tx)
	case 55:
		return migrateToV55(tx)
	case 56:
		return migrateToV56(tx)
	case 57:
		return migrateToV57(tx)
	case 58:
		return migrateToV58(tx)
	case 59:
		return migrateToV59(tx)
	case 60:
		return migrateToV60(tx)
	case 61:
		return migrateToV61(tx)
	case 62:
		return migrateToV62(tx)
	case 63:
		return migrateToV63(tx)
	case 64:
		return migrateToV64(tx)
	case 65:
		return migrateToV65(tx)
	case 66:
		return migrateToV66(tx)
	case 67:
		return migrateToV67(tx)
	case 68:
		return migrateToV68(tx)
	case 69:
		return migrateToV69(tx)
	case 70:
		return migrateToV70(tx)
	case 71:
		return migrateToV71(tx)
	case 72:
		return migrateToV72(tx)
	case 73:
		return migrateToV73(tx)
	case 74:
		return migrateToV74(tx)
	case 75:
		return migrateToV75(tx)
	case 76:
		return migrateToV76(tx)
	case 77:
		return migrateToV77(tx)
	case 78:
		return migrateToV78(tx)
	case 79:
		return migrateToV79(tx)
	case 80:
		return migrateToV80(tx)
	case 81:
		return migrateToV81(tx)
	case 82:
		return migrateToV82(tx)
	case 83:
		return migrateToV83(tx)
	case 84:
		return migrateToV84(tx)
	case 85:
		return migrateToV85(tx)
	case 86:
		return migrateToV86(tx)
	case 87:
		return migrateToV87(tx)
	case 88:
		return migrateToV88(tx)
	case 89:
		return migrateToV89(tx)
	case 90:
		return migrateToV90(tx)
	case 91:
		return migrateToV91(tx)
	case 92:
		return migrateToV92(tx)
	case 93:
		return migrateToV93(tx)
	case 94:
		if err := migrateToV74(tx); err != nil {
			return err
		}
		if err := addColumnIfMissing(tx, "inbox_item", "category", "TEXT NOT NULL DEFAULT 'general'"); err != nil {
			return err
		}
		return addColumnIfMissing(tx, "inbox_item", "permission_json", "TEXT NOT NULL DEFAULT ''")
	case 95:
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS factory_workflow_step (
			issue_id TEXT PRIMARY KEY REFERENCES factory_issue(id),
			definition_json TEXT NOT NULL
		)`)
		return err
	case 96:
		return migrateToV96(tx)
	case 97:
		return migrateToV97(tx)
	case 98:
		return addColumnIfMissing(tx, "inbox_item", "session_json", "TEXT NOT NULL DEFAULT ''")
	case 99:
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='factory_epic')`).Scan(&exists); err != nil || !exists {
			return err
		}
		return addColumnIfMissing(tx, "factory_epic", "models_json", "TEXT NOT NULL DEFAULT '{}'")
	default:
		return fmt.Errorf("no migration registered for v%d", target)
	}
}
