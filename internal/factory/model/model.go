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

type FactoryAttemptPhase string

const (
	FactoryAttemptPrepared FactoryAttemptPhase = "prepared"
	FactoryAttemptActive   FactoryAttemptPhase = "active"
	FactoryAttemptStopping FactoryAttemptPhase = "stopping"
	FactoryAttemptTerminal FactoryAttemptPhase = "terminal"
)

type FactoryAttemptOutcome string

const (
	FactoryAttemptSucceeded           FactoryAttemptOutcome = "succeeded"
	FactoryAttemptSkipped             FactoryAttemptOutcome = "skipped"
	FactoryAttemptCancelled           FactoryAttemptOutcome = "cancelled"
	FactoryAttemptAcknowledgedPartial FactoryAttemptOutcome = "acknowledged_partial"
	FactoryAttemptFailed              FactoryAttemptOutcome = "failed"
	FactoryAttemptInterrupted         FactoryAttemptOutcome = "interrupted"
	FactoryAttemptAmbiguous           FactoryAttemptOutcome = "ambiguous"
)

type FactoryAttemptPolicy struct {
	PlanRevision int    `json:"planRevision"`
	PlanHash     string `json:"planHash"`
	TargetID     string `json:"targetId"`
	Repository   string `json:"repository"`
	Profile      string `json:"profile"`
}

type FactoryAttemptResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	Summary       string `json:"summary"`
}

type FactoryAttemptFailure struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type FactoryAttempt struct {
	ID               string
	EpicID           string
	WorkID           string
	Sequence         int
	Phase            FactoryAttemptPhase
	Outcome          FactoryAttemptOutcome
	Session          PlanningSession
	RetryOfAttemptID string
	RetryAt          int64
	FrozenPolicy     FactoryAttemptPolicy
	Result           *FactoryAttemptResult
	Failure          FactoryAttemptFailure
	CreatedAt        int64
	UpdatedAt        int64
	StartedAt        int64
	FinishedAt       int64
}

type AuditRecord struct {
	EpicID    string
	WorkID    string
	AttemptID string
	Actor     string
	Action    string
	Details   any
	At        time.Time
}
