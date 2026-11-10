package platformconnections

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// Reservation pins the owner's configuration choice through setup retries and
// restart. It is not an account observation or execution authorization.
type Reservation struct {
	ID, ConnectionID, Kind, Configuration, Directory, Status string
}

// Reserve records owner-resolved metadata before setup can create a directory.
// Pending reservations consume capacity, just like nonremoved connections.
func (s *Store) Reserve(ctx context.Context, kind, configuration, directory string) (Reservation, error) {
	input, err := validate(Input{Kind: kind, Name: "setup", Configuration: configuration, Directory: directory}, false)
	if err != nil {
		return Reservation{}, err
	}
	r := Reservation{ID: "stp_" + uuid.NewString(), ConnectionID: "cnx_" + uuid.NewString(),
		Kind: input.Kind, Configuration: input.Configuration, Directory: input.Directory, Status: "pending"}
	result, err := s.db.ExecContext(ctx, `INSERT INTO platform_connection_setup(id,connection_id,kind,configuration,directory)
		SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM platform_connection_collection WHERE id=1)
		AND (SELECT count(*) FROM platform_connection WHERE removed=0)
		+ (SELECT count(*) FROM platform_connection_setup WHERE status='pending')<?`, r.ID, r.ConnectionID, r.Kind, r.Configuration, r.Directory, maxConnections)
	if err != nil {
		return Reservation{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 0 {
		return r, err
	}
	return Reservation{}, s.capacityError(ctx)
}

func reservation(row interface{ Scan(...any) error }) (Reservation, error) {
	var r Reservation
	err := row.Scan(&r.ID, &r.ConnectionID, &r.Kind, &r.Configuration, &r.Directory, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

const reservationColumns = `id,connection_id,kind,configuration,directory,status`

func (s *Store) Reservation(ctx context.Context, id string) (Reservation, error) {
	return reservation(s.db.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM platform_connection_setup WHERE id=?`, id))
}

// CancelReservation releases capacity but keeps the receipt and configuration.
// Even an unfinished login may have written credentials. Never delete its files.
// A saved receipt cannot be canceled to detach a connection without draining it.
func (s *Store) CancelReservation(ctx context.Context, id string) error {
	return affected(s.db.ExecContext(ctx, `UPDATE platform_connection_setup SET status='canceled' WHERE id=? AND status!='saved'`, id))
}

// SaveReservation consumes only owner-pinned metadata. Browser observations,
// paths and platform choices are not accepted at Save time. Enabled is intent;
// the owner must still check readiness before executing through the connection.
// A repeated Save returns the current connection without rewriting it.
func (s *Store) SaveReservation(ctx context.Context, id, name string, enabled bool) (Connection, error) {
	name, err := validateName(name)
	if err != nil {
		return Connection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Connection{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Acquire the write lock before reading, also across separate DB handles.
	if err := affected(tx.ExecContext(ctx, `UPDATE platform_connection_setup SET status=status WHERE id=?`, id)); err != nil {
		return Connection{}, err
	}
	r, err := reservation(tx.QueryRowContext(ctx, `SELECT `+reservationColumns+` FROM platform_connection_setup WHERE id=?`, id))
	if err != nil {
		return Connection{}, err
	}
	if r.Status == "canceled" {
		return Connection{}, ErrUnavailable
	}
	if r.Status == "saved" {
		c, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM platform_connection WHERE id=?`, r.ConnectionID))
		if err == nil && c.Removed {
			err = ErrUnavailable
		}
		return c, err
	}
	c := Connection{ID: r.ConnectionID, Kind: r.Kind, Name: name, Configuration: r.Configuration, Directory: r.Directory, Enabled: enabled}
	if _, err := tx.ExecContext(ctx, `INSERT INTO platform_connection(id,kind,name,configuration,directory,enabled)
		VALUES(?,?,?,?,?,?)`, c.ID, c.Kind, c.Name, c.Configuration, c.Directory, c.Enabled); err != nil {
		return Connection{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE platform_connection_setup SET status='saved' WHERE id=?`, id); err != nil {
		return Connection{}, err
	}
	if err := tx.Commit(); err != nil {
		return Connection{}, err
	}
	return c, nil
}
