package model

import "time"

type Formula struct {
	ID              string
	Name            string
	Source          string
	CurrentRevision int
	ArchivedAt      int64
	CreatedAt       int64
	UpdatedAt       int64
}

type FormulaRevision struct {
	FormulaID      string
	Revision       int
	SchemaVersion  int
	DefinitionYAML string
	ContentHash    string
	ValidationJSON string
	CreatedAt      int64
}

type PlanningSession struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
}

type AuditRecord struct {
	EpicID  string
	WorkID  string
	Actor   string
	Action  string
	Details any
	At      time.Time
}
