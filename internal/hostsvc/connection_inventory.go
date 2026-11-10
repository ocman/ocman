package hostsvc

import (
	"context"
	"github.com/ocman/ocman/internal/platformconnections"
)

// Inspection contains display-safe observations, never credentials.
type ConnectionCheck struct {
	ReservationID string                `json:"reservationId,omitempty"`
	Status        string                `json:"status"`
	Account       string                `json:"account,omitempty"`
	Directory     string                `json:"directory,omitempty"`
	Checks        []ConnectionCheckItem `json:"checks"`
	Instructions  string                `json:"instructions,omitempty"`
	Command       string                `json:"command,omitempty"`
}
type ConnectionCheckItem struct {
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type ConnectionView struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Configuration string `json:"configuration"`
	Directory     string `json:"directory"`
	Enabled       bool   `json:"enabled"`
	IsDefault     bool   `json:"isDefault"`
	Status        string `json:"status"`
	Account       string `json:"account,omitempty"`
}
type ConnectionPlatform struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	ExistingDirectory string `json:"existingDirectory"`
	SupportsIsolation bool   `json:"supportsIsolation"`
	IsolationNote     string `json:"isolationNote"`
}
type ConnectionInventory struct {
	Supported          bool                 `json:"supported"`
	OnboardingComplete bool                 `json:"onboardingComplete"`
	Connections        []ConnectionView     `json:"connections"`
	Platforms          []ConnectionPlatform `json:"platforms"`
}
type ConnectionCheckRequest struct {
	Directory     string `json:"directory,omitempty"`
	ID            string `json:"id,omitempty"`
	Kind          string `json:"kind"`
	Configuration string `json:"configuration"`
	ReservationID string `json:"reservationId,omitempty"`
}
type ConnectionUpdate struct {
	ID      string  `json:"id"`
	Name    *string `json:"name,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
	Default bool    `json:"default,omitempty"`
}

// ConnectionHost is optional. Unsupported owners must not advertise setup UI.
type ConnectionHost interface {
	ConnectionSetupHost
	InventoryPlatformConnections(context.Context) (ConnectionInventory, error)
	CheckPlatformConnection(context.Context, ConnectionCheckRequest) (ConnectionCheck, error)
	UpdatePlatformConnection(context.Context, ConnectionUpdate) error
	RemovePlatformConnection(context.Context, string) error
	CompletePlatformConnectionOnboarding(context.Context) error
}

// Nil inspection is unchecked; nil detach refuses removal. Detach must close
// admission and drain before invoking remove, restoring admission on failure.
type ConnectionLifecycle struct {
	Inspect func(context.Context, platformconnections.Connection) (ConnectionCheck, error)
	// Activate publishes this exact saved configuration. It must be idempotent.
	// Failure leaves the saved identity intact so retry never allocates a sibling.
	Activate func(context.Context, platformconnections.Connection) error
	Detach   func(context.Context, platformconnections.Connection, func() error) error
}
