// Package convevents defines Ocman conversation events (OCE): the versioned,
// platform-agnostic per-session stream described in
// spec/conversation-events/design.md. It holds wire types, the type registry
// and stateless helpers only; producers and consumers live elsewhere.
package convevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const Version = 1

type Durability string

const (
	Durable   Durability = "durable"
	Live      Durability = "live"
	Ephemeral Durability = "ephemeral"
)

type Envelope struct {
	V         int    `json:"v"`
	Epoch     string `json:"epoch"`
	Seq       uint64 `json:"seq"`
	SessionID string `json:"sessionId"`
	Platform  string `json:"platform"`
	At        int64  `json:"at"`
	// Durability is filled from Event by MarshalJSON when empty.
	Durability Durability `json:"durability"`
	Replayed   bool       `json:"replayed,omitempty"`
	Event      Event      `json:"event"`
}

// Event is a closed, sealed set. Unknown types decode to Unknown and are
// ignored by consumers (forward compatibility within v1).
type Event interface {
	EventType() string
	durability() Durability
}

// DurabilityOf returns the durability class of ev's type.
func DurabilityOf(ev Event) Durability { return ev.durability() }

// Cursor is the resume position "<epoch>:<seq>".
func (e Envelope) Cursor() string { return FormatCursor(e.Epoch, e.Seq) }

func FormatCursor(epoch string, seq uint64) string {
	return epoch + ":" + strconv.FormatUint(seq, 10)
}

// ParseCursor splits "<epoch>:<seq>". The epoch may itself contain colons.
func ParseCursor(s string) (epoch string, seq uint64, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i <= 0 {
		return "", 0, false
	}
	seq, err := strconv.ParseUint(s[i+1:], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return s[:i], seq, true
}

type envelopeWire struct {
	V          int             `json:"v"`
	Epoch      string          `json:"epoch"`
	Seq        uint64          `json:"seq"`
	SessionID  string          `json:"sessionId"`
	Platform   string          `json:"platform"`
	At         int64           `json:"at"`
	Durability Durability      `json:"durability"`
	Replayed   bool            `json:"replayed,omitempty"`
	Event      json.RawMessage `json:"event"`
}

func (e Envelope) MarshalJSON() ([]byte, error) {
	if e.Event == nil {
		return nil, errors.New("convevents: envelope without event")
	}
	ev, err := json.Marshal(e.Event)
	if err != nil {
		return nil, err
	}
	w := envelopeWire{V: e.V, Epoch: e.Epoch, Seq: e.Seq, SessionID: e.SessionID, Platform: e.Platform,
		At: e.At, Durability: e.Durability, Replayed: e.Replayed, Event: ev}
	if w.V == 0 {
		w.V = Version
	}
	if w.Durability == "" {
		w.Durability = e.Event.durability()
	}
	return json.Marshal(w)
}

func (e *Envelope) UnmarshalJSON(b []byte) error {
	var w envelopeWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	ev, err := Decode(w.Event)
	if err != nil {
		return err
	}
	*e = Envelope{V: w.V, Epoch: w.Epoch, Seq: w.Seq, SessionID: w.SessionID, Platform: w.Platform,
		At: w.At, Durability: w.Durability, Replayed: w.Replayed, Event: ev}
	return nil
}

// Decode reads one event object. A type outside the registry yields Unknown
// without error; a registered type with malformed fields is an error.
func Decode(raw []byte) (Event, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("convevents: event: %w", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return nil, errors.New("convevents: event is not an object")
	}
	reg, ok := registrations[head.Type]
	if !ok {
		return Unknown{Type: head.Type, Raw: append(json.RawMessage(nil), raw...)}, nil
	}
	ev, err := reg.decode(raw)
	if err != nil {
		return nil, fmt.Errorf("convevents: %s: %w", head.Type, err)
	}
	return ev, nil
}

// Unknown is an event type this build does not know. Consumers ignore it.
type Unknown struct {
	Type string
	Raw  json.RawMessage
}

func (u Unknown) EventType() string      { return u.Type }
func (u Unknown) durability() Durability { return "" }

func (u Unknown) MarshalJSON() ([]byte, error) {
	if len(u.Raw) == 0 {
		return json.Marshal(map[string]string{"type": u.Type})
	}
	return u.Raw, nil
}
