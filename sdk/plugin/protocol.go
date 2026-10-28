// Package plugin provides optional Go helpers for ocman's NDJSON plugin protocol.
// Other languages can implement the same wire contract without this SDK.
package plugin

import (
	"io"

	"github.com/ocman/ocman/sdk/protocol"
)

// Aliases keep the SDK and host on the same canonical DTOs and validators.
type (
	Mode             = protocol.Mode
	Scope            = protocol.Scope
	Version          = protocol.Version
	Capability       = protocol.Capability
	Description      = protocol.Description
	Setting          = protocol.Setting
	MessageType      = protocol.MessageType
	Envelope         = protocol.Envelope
	Hello            = protocol.Hello
	Negotiated       = protocol.Negotiated
	Call             = protocol.Call
	Chunk            = protocol.Chunk
	Result           = protocol.Result
	Event            = protocol.Event
	Cancel           = protocol.Cancel
	Shutdown         = protocol.Shutdown
	WireError        = protocol.WireError
	ErrorCategory    = protocol.ErrorCategory
	ActionDescriptor = protocol.ActionDescriptor
	ActionContext    = protocol.ActionContext
	ActionSelection  = protocol.ActionSelection
	ActionInvocation = protocol.ActionInvocation
	ActionResult     = protocol.ActionResult
	Decoder          = protocol.Decoder
	Encoder          = protocol.Encoder
	Stream           = protocol.Stream
	Direction        = protocol.Direction
)

const (
	ProtocolMajor         = protocol.ProtocolMajor
	ProtocolMinor         = protocol.ProtocolMinor
	MaxMessageBytes       = protocol.MaxMessageBytes
	MaxConcurrency        = protocol.MaxConcurrency
	ModeDescribe          = protocol.ModeDescribe
	ModeServe             = protocol.ModeServe
	ScopeHub              = protocol.ScopeHub
	ScopeOwner            = protocol.ScopeOwner
	ScopeGlobal           = protocol.ScopeGlobal
	TypeHello             = protocol.TypeHello
	TypeCall              = protocol.TypeCall
	TypeChunk             = protocol.TypeChunk
	TypeResult            = protocol.TypeResult
	TypeEvent             = protocol.TypeEvent
	TypeCancel            = protocol.TypeCancel
	TypeShutdown          = protocol.TypeShutdown
	FromHost              = protocol.FromHost
	FromPlugin            = protocol.FromPlugin
	ErrorInvalidArgument  = protocol.ErrorInvalidArgument
	ErrorPermissionDenied = protocol.ErrorPermissionDenied
	ErrorNotFound         = protocol.ErrorNotFound
	ErrorConflict         = protocol.ErrorConflict
	ErrorUnavailable      = protocol.ErrorUnavailable
	ErrorDeadlineExceeded = protocol.ErrorDeadlineExceeded
	ErrorCancelled        = protocol.ErrorCancelled
	ErrorInternal         = protocol.ErrorInternal
)

var (
	ErrInvalidMessage      = protocol.ErrInvalidMessage
	ErrMessageTooLarge     = protocol.ErrMessageTooLarge
	ErrIncompatibleVersion = protocol.ErrIncompatibleVersion
	ErrStreamOrder         = protocol.ErrStreamOrder
	ErrHandshake           = protocol.ErrHandshake
)

func NewDecoder(r io.Reader) *Decoder { return protocol.NewDecoder(r) }
func NewEncoder(w io.Writer) *Encoder { return protocol.NewEncoder(w) }
func NewStream(mode Mode, token string, version Version, supported []Capability) (*Stream, error) {
	return protocol.NewStream(mode, token, version, supported)
}
func Negotiate(version Version, supported []Capability, remote Description) (Negotiated, error) {
	return protocol.Negotiate(version, supported, remote)
}

func DecodeActionResults(data []byte) ([]ActionResult, error) {
	return protocol.DecodeActionResults(data)
}
