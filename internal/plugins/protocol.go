// Package plugins defines ocman's external plugin wire protocol. Wire DTOs must
// not embed application, database, platform, or capability implementation types.
package plugins

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ProtocolMajor   = 1
	ProtocolMinor   = 0
	MaxMessageBytes = 1 << 20 // JSON bytes, excluding the terminating LF or CRLF.
	MaxConcurrency  = 256
	maxJSONDepth    = 64
)

var (
	ErrInvalidMessage      = errors.New("invalid plugin message")
	ErrMessageTooLarge     = errors.New("plugin message exceeds size limit")
	ErrIncompatibleVersion = errors.New("incompatible plugin protocol version")
	ErrStreamOrder         = errors.New("invalid plugin stream order")
	ErrHandshake           = errors.New("invalid plugin handshake")
)

type Mode string

const (
	ModeDescribe Mode = "describe"
	ModeServe    Mode = "serve"
)

type Scope string

const (
	ScopeHub    Scope = "hub"
	ScopeOwner  Scope = "owner"
	ScopeGlobal Scope = "global"
)

// Version uses major for breaking changes and minor for additive changes.
// Process and capability versions are negotiated independently.
type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

type Capability struct {
	Name    string  `json:"name"`
	Version Version `json:"version"`
}

// Description is returned in the single hello from both describe and serve.
// Capability-specific descriptors are owned by their separately versioned DTOs.
type Description struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Protocol        Version            `json:"protocol"`
	Capabilities    []Capability       `json:"capabilities,omitempty"`
	MaxConcurrency  int                `json:"maxConcurrency"`
	Scope           Scope              `json:"scope"`
	RequestedGrants []string           `json:"requestedGrants,omitempty"`
	Settings        []Setting          `json:"settings,omitempty"`
	Actions         []ActionDescriptor `json:"actions,omitempty"`
}

type MessageType string

const (
	TypeHello    MessageType = "hello"
	TypeCall     MessageType = "call"
	TypeChunk    MessageType = "chunk"
	TypeResult   MessageType = "result"
	TypeEvent    MessageType = "event"
	TypeCancel   MessageType = "cancel"
	TypeShutdown MessageType = "shutdown"
)

// Envelope contains exactly one body, whose key matches Type. Unknown additive
// fields are ignored; unknown message types and mixed known bodies are errors.
type Envelope struct {
	Type     MessageType `json:"type"`
	Hello    *Hello      `json:"hello,omitempty"`
	Call     *Call       `json:"call,omitempty"`
	Chunk    *Chunk      `json:"chunk,omitempty"`
	Result   *Result     `json:"result,omitempty"`
	Event    *Event      `json:"event,omitempty"`
	Cancel   *Cancel     `json:"cancel,omitempty"`
	Shutdown *Shutdown   `json:"shutdown,omitempty"`
}

type Hello struct {
	Mode        Mode         `json:"mode"`
	Token       string       `json:"token,omitempty"`       // Plugin echoes the per-launch 32-byte random token, lowercase hex.
	Description *Description `json:"description,omitempty"` // Plugin's offer.
	Accepted    *Negotiated  `json:"accepted,omitempty"`    // Host's serve-mode acknowledgment, without a token.
}

// Request IDs are strictly increasing positive decimal strings on the wire.
// OperationID is an opaque idempotency key, retained across retries with new IDs.
// DeadlineUnixMS is an absolute UTC Unix millisecond deadline. The supervisor
// enforces elapsed deadlines; the codec only validates the representation.
type Call struct {
	ID             uint64          `json:"id,string"`
	OperationID    string          `json:"operationId"`
	Capability     string          `json:"capability"`
	Version        Version         `json:"version"` // Negotiated capability version, not process version.
	Method         string          `json:"method"`
	DeadlineUnixMS int64           `json:"deadlineUnixMs"`
	Params         json.RawMessage `json:"params"`
}

type Chunk struct {
	ID       uint64          `json:"id,string"`
	Sequence uint64          `json:"sequence"` // Contiguous, starting at one, separately for each call.
	Data     json.RawMessage `json:"data"`
}

type Result struct {
	ID    uint64          `json:"id,string"`
	Value json.RawMessage `json:"value,omitempty"`
	Error *WireError      `json:"error,omitempty"`
}

// WireError intentionally has no free-form message or details that could expose
// credentials, filesystem paths, or plugin stderr. The host supplies UI text.
type WireError struct {
	Category ErrorCategory `json:"category"`
}

type ErrorCategory string

const (
	ErrorInvalidArgument  ErrorCategory = "invalid_argument"
	ErrorPermissionDenied ErrorCategory = "permission_denied"
	ErrorNotFound         ErrorCategory = "not_found"
	ErrorConflict         ErrorCategory = "conflict"
	ErrorUnavailable      ErrorCategory = "unavailable"
	ErrorDeadlineExceeded ErrorCategory = "deadline_exceeded"
	ErrorCancelled        ErrorCategory = "cancelled"
	ErrorInternal         ErrorCategory = "internal"
)

type Event struct {
	Capability string          `json:"capability"`
	Name       string          `json:"name"`
	Data       json.RawMessage `json:"data"`
}

type Cancel struct {
	ID uint64 `json:"id,string"`
}

type Shutdown struct{}

var (
	identifier = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	pluginID   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)+$`)
)

func validText(s string) bool {
	return s != "" && len(s) <= 128 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

func validName(s string) bool     { return len(s) <= 128 && identifier.MatchString(s) }
func validVersion(v Version) bool { return v.Major > 0 && v.Minor >= 0 }
func validMode(m Mode) bool       { return m == ModeDescribe || m == ModeServe }
func validToken(token string) bool {
	if len(token) != 64 || token != strings.ToLower(token) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func validateCapabilities(caps []Capability) error {
	seen := make(map[string]bool, len(caps))
	for _, c := range caps {
		if !validName(c.Name) || !validVersion(c.Version) || seen[c.Name] {
			return ErrInvalidMessage
		}
		seen[c.Name] = true
	}
	return nil
}

func (d Description) Validate() error {
	if len(d.ID) > 253 || !pluginID.MatchString(d.ID) || !validText(d.Name) || !validText(d.Version) ||
		!validVersion(d.Protocol) || d.MaxConcurrency < 1 || d.MaxConcurrency > MaxConcurrency ||
		(d.Scope != ScopeHub && d.Scope != ScopeOwner && d.Scope != ScopeGlobal) {
		return ErrInvalidMessage
	}
	if err := validateCapabilities(d.Capabilities); err != nil {
		return err
	}
	if err := validateSettingsAndGrants(d.Settings, d.RequestedGrants); err != nil {
		return err
	}
	if err := d.validateActions(); err != nil {
		return err
	}
	return d.validateConversation()
}

func (e Envelope) Validate() error {
	bodies := map[MessageType]bool{
		TypeHello: e.Hello != nil, TypeCall: e.Call != nil, TypeChunk: e.Chunk != nil,
		TypeResult: e.Result != nil, TypeEvent: e.Event != nil, TypeCancel: e.Cancel != nil, TypeShutdown: e.Shutdown != nil,
	}
	count := 0
	for _, present := range bodies {
		if present {
			count++
		}
	}
	if count != 1 || !bodies[e.Type] {
		return ErrInvalidMessage
	}
	switch e.Type {
	case TypeHello:
		h := e.Hello
		if !validMode(h.Mode) || (h.Description != nil) == (h.Accepted != nil) {
			return ErrInvalidMessage
		}
		if h.Description != nil {
			if !validToken(h.Token) {
				return ErrInvalidMessage
			}
			return h.Description.Validate()
		}
		if h.Mode != ModeServe || h.Token != "" || !validVersion(h.Accepted.Protocol) {
			return ErrInvalidMessage
		}
		return validateCapabilities(h.Accepted.Capabilities)
	case TypeCall:
		c := e.Call
		if c.ID == 0 || !validText(c.OperationID) || !validName(c.Capability) || !validVersion(c.Version) || !validName(c.Method) || c.DeadlineUnixMS <= 0 || !json.Valid(c.Params) {
			return ErrInvalidMessage
		}
	case TypeChunk:
		if e.Chunk.ID == 0 || e.Chunk.Sequence == 0 || !json.Valid(e.Chunk.Data) {
			return ErrInvalidMessage
		}
	case TypeResult:
		r := e.Result
		if r.ID == 0 || (len(r.Value) > 0) == (r.Error != nil) {
			return ErrInvalidMessage
		}
		if r.Error == nil {
			if !json.Valid(r.Value) {
				return ErrInvalidMessage
			}
		} else {
			switch r.Error.Category {
			case ErrorInvalidArgument, ErrorPermissionDenied, ErrorNotFound, ErrorConflict, ErrorUnavailable, ErrorDeadlineExceeded, ErrorCancelled, ErrorInternal:
			default:
				return ErrInvalidMessage
			}
		}
	case TypeEvent:
		if !validName(e.Event.Capability) || !validName(e.Event.Name) || !json.Valid(e.Event.Data) {
			return ErrInvalidMessage
		}
	case TypeCancel:
		if e.Cancel.ID == 0 {
			return ErrInvalidMessage
		}
	}
	return nil
}

// Negotiated is the host's selected versions, acknowledged in its serve hello.
// Unsupported capability majors are omitted, not a process-level failure.
type Negotiated struct {
	Protocol     Version      `json:"protocol"`
	Capabilities []Capability `json:"capabilities,omitempty"`
}

func Negotiate(protocol Version, supported []Capability, remote Description) (Negotiated, error) {
	if !validVersion(protocol) || validateCapabilities(supported) != nil || remote.Validate() != nil {
		return Negotiated{}, ErrInvalidMessage
	}
	if protocol.Major != remote.Protocol.Major {
		return Negotiated{}, ErrIncompatibleVersion
	}
	n := Negotiated{Protocol: Version{protocol.Major, min(protocol.Minor, remote.Protocol.Minor)}}
	for _, r := range remote.Capabilities {
		for _, local := range supported {
			if local.Name == r.Name && local.Version.Major == r.Version.Major {
				n.Capabilities = append(n.Capabilities, Capability{r.Name, Version{r.Version.Major, min(local.Version.Minor, r.Version.Minor)}})
			}
		}
	}
	return n, nil
}
