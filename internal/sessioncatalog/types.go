// Package sessioncatalog owns durable Ocman session identities independently
// of platform execution and conversation-history storage.
package sessioncatalog

import (
	"database/sql"
	"errors"
	"strings"
)

var (
	ErrNotFound        = errors.New("ocman session not found")
	ErrInvalid         = errors.New("invalid session catalogue input")
	ErrRequestConflict = errors.New("session creation request changed")
	ErrBindingConflict = errors.New("native session already has an Ocman identity")
	ErrTransition      = errors.New("invalid session creation transition")
	ErrCreating        = errors.New("session creation is in progress")
	ErrCreationFailed  = errors.New("session creation failed")
)

type CreationState string

const (
	Creating       CreationState = "creating"
	Configuring    CreationState = "configuring"
	Ready          CreationState = "ready"
	CreationFailed CreationState = "creation-failed"
)

// Origin is assigned before any platform side effects, so list visibility
// never depends on a later routine/factory association lookup.
type Origin string

const (
	User     Origin = "user"
	Routine  Origin = "routine"
	Factory  Origin = "factory"
	Plugin   Origin = "plugin"
	External Origin = "external"
)

type Session struct {
	ID             string `json:"id"`
	OwnerID        string `json:"ownerId"`
	Platform       string `json:"platform"`
	Directory      string `json:"directory"`
	Title          string `json:"title"`
	ParentID       string `json:"parentId,omitempty"`
	NativeParentID string `json:"nativeParentId,omitempty"`
	Purged         bool   `json:"purged,omitempty"`
	// LegacyKeys keeps pre-cutover deduplication identities inside adapters.
	LegacyKeys    bool          `json:"-"`
	Origin        Origin        `json:"origin"`
	OriginID      string        `json:"originId,omitempty"`
	CreationState CreationState `json:"creationState"`
	CreationError string        `json:"creationError,omitempty"`
	SourceKey     string        `json:"sourceKey,omitempty"`
	NativeID      string        `json:"nativeId,omitempty"`
	CreatedAt     int64         `json:"createdAt"`
	UpdatedAt     int64         `json:"updatedAt"`
}

type Reservation struct {
	RequestKey string
	// PayloadHash is the SHA-256 of the complete creation request, including
	// execution configuration not stored as catalogue metadata. Reusing a key
	// with different permission rules or launch input must not look like a replay.
	PayloadHash string
	OwnerID     string
	Platform    string
	Directory   string
	Title       string
	ParentID    string
	Origin      Origin
	OriginID    string
}

// Binding names a native identity in a physical source on its owning machine.
// A native ID alone, or an owner/platform without its source, is ambiguous.
type Binding struct {
	OwnerID   string
	Platform  string
	SourceKey string
	NativeID  string
}

type Source struct {
	OwnerID   string
	Platform  string
	SourceKey string
}

// NativeSession is owner-local discovery metadata. ParentID is native and
// scoped to the same physical source; it is never used as an Ocman route ID.
type NativeSession struct {
	ID        string
	CreatedAt int64 // Native creation time in Unix milliseconds; zero means unknown.
	ParentID  string
	Directory string
	Title     string
	Origin    Origin
	OriginID  string
}

type ReconcileResult struct {
	Created  int
	Deferred int
}

func nonempty(s string) bool { return strings.TrimSpace(s) != "" }

func (b Binding) valid() bool {
	return nonempty(b.OwnerID) && nonempty(b.Platform) && nonempty(b.SourceKey) && nonempty(b.NativeID)
}

type Store struct{ db *sql.DB }
