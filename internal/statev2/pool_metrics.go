package statev2

import (
	"database/sql"

	"github.com/XSAM/otelsql"
	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
)

func (d *DB) registerPoolMetrics() {
	for _, pool := range []struct {
		name string
		conn *sql.DB
	}{{"statev2", d.db}, {"statev2-read", d.read}} {
		registration, err := otelsql.RegisterDBStatsMetrics(pool.conn, otelsql.WithAttributes(attribute.String("db.name", pool.name)))
		if err != nil {
			log.WithError(err).WithField("db", pool.name).Warn("conversation pool metrics unavailable")
			continue
		}
		d.poolMetrics = append(d.poolMetrics, registration)
	}
}
