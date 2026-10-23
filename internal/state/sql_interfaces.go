package state

import (
	"context"
	"database/sql"
)

// Shared by *sql.DB and *sql.Tx, so reads can join a caller's transaction.
type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type rowsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type rowScanner interface{ Scan(...any) error }
