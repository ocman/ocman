package notifications

import "database/sql"

func migrateDeliveryIndexes(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE INDEX push_delivery_retention ON push_delivery(created_at);
		CREATE INDEX push_delivery_due ON push_delivery(next_at,created_at) WHERE done=0;`)
	return err
}
