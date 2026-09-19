// Package plugin provides optional Go helpers for ocman's NDJSON plugin protocol.
// Other languages can implement the same wire contract without this SDK.
package plugin

import (
	"io"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

// Aliases keep the SDK and host on the same canonical DTOs and validators.
type (
	Mode             = plugins.Mode
	Scope            = plugins.Scope
	Version          = plugins.Version
	Capability       = plugins.Capability
	Description      = plugins.Description
	Setting          = plugins.Setting
	MessageType      = plugins.MessageType
	Envelope         = plugins.Envelope
	Hello            = plugins.Hello
	Negotiated       = plugins.Negotiated
	Call             = plugins.Call
	Chunk            = plugins.Chunk
	Result           = plugins.Result
	Event            = plugins.Event
	Cancel           = plugins.Cancel
	Shutdown         = plugins.Shutdown
	WireError        = plugins.WireError
	ErrorCategory    = plugins.ErrorCategory
	ActionDescriptor = plugins.ActionDescriptor
	ActionContext    = plugins.ActionContext
	ActionSelection  = plugins.ActionSelection
	ActionInvocation = plugins.ActionInvocation
	ActionResult     = plugins.ActionResult
	Decoder          = plugins.Decoder
	Encoder          = plugins.Encoder
	Stream           = plugins.Stream
	Direction        = plugins.Direction
)

const (
	ProtocolMajor         = plugins.ProtocolMajor
	ProtocolMinor         = plugins.ProtocolMinor
	MaxMessageBytes       = plugins.MaxMessageBytes
	MaxConcurrency        = plugins.MaxConcurrency
	ModeDescribe          = plugins.ModeDescribe
	ModeServe             = plugins.ModeServe
	ScopeHub              = plugins.ScopeHub
	ScopeOwner            = plugins.ScopeOwner
	ScopeGlobal           = plugins.ScopeGlobal
	TypeHello             = plugins.TypeHello
	TypeCall              = plugins.TypeCall
	TypeChunk             = plugins.TypeChunk
	TypeResult            = plugins.TypeResult
	TypeEvent             = plugins.TypeEvent
	TypeCancel            = plugins.TypeCancel
	TypeShutdown          = plugins.TypeShutdown
	FromHost              = plugins.FromHost
	FromPlugin            = plugins.FromPlugin
	ErrorInvalidArgument  = plugins.ErrorInvalidArgument
	ErrorPermissionDenied = plugins.ErrorPermissionDenied
	ErrorNotFound         = plugins.ErrorNotFound
	ErrorConflict         = plugins.ErrorConflict
	ErrorUnavailable      = plugins.ErrorUnavailable
	ErrorDeadlineExceeded = plugins.ErrorDeadlineExceeded
	ErrorCancelled        = plugins.ErrorCancelled
	ErrorInternal         = plugins.ErrorInternal
)

var (
	ErrInvalidMessage      = plugins.ErrInvalidMessage
	ErrMessageTooLarge     = plugins.ErrMessageTooLarge
	ErrIncompatibleVersion = plugins.ErrIncompatibleVersion
	ErrStreamOrder         = plugins.ErrStreamOrder
	ErrHandshake           = plugins.ErrHandshake
)

func NewDecoder(r io.Reader) *Decoder { return plugins.NewDecoder(r) }
func NewEncoder(w io.Writer) *Encoder { return plugins.NewEncoder(w) }
func NewStream(mode Mode, token string, protocol Version, supported []Capability) (*Stream, error) {
	return plugins.NewStream(mode, token, protocol, supported)
}
func Negotiate(protocol Version, supported []Capability, remote Description) (Negotiated, error) {
	return plugins.Negotiate(protocol, supported, remote)
}

func DecodeActionResults(data []byte) ([]ActionResult, error) {
	return plugins.DecodeActionResults(data)
}
