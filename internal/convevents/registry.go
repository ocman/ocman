package convevents

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Type strings of the closed v1 event set.
const (
	TypeStreamHello       = "stream.hello"
	TypeStreamReset       = "stream.reset"
	TypeStreamError       = "stream.error"
	TypeMessageUpserted   = "message.upserted"
	TypeMessageRemoved    = "message.removed"
	TypePartUpserted      = "part.upserted"
	TypePartRemoved       = "part.removed"
	TypePartDelta         = "part.delta"
	TypeSessionStatus     = "session.status"
	TypeTurnStarted       = "turn.started"
	TypeTurnEnded         = "turn.ended"
	TypeSessionUpdated    = "session.updated"
	TypeHistoryRewritten  = "history.rewritten"
	TypeHistoryCommitted  = "history.committed"
	TypePromptAsked       = "prompt.asked"
	TypePromptJudge       = "prompt.judge"
	TypePromptResolved    = "prompt.resolved"
	TypeQueueChanged      = "queue.changed"
	TypeBackgroundChanged = "background.changed"
	TypeChildUpserted     = "child.upserted"
	TypeChildStatus       = "child.status"
	TypeChangesDirty      = "changes.dirty"
	TypeSessionError      = "session.error"
)

type registration struct {
	durability Durability
	decode     func([]byte) (Event, error)
}

func decodeAs[T Event](raw []byte) (Event, error) {
	var v T
	err := json.Unmarshal(raw, &v)
	return v, err
}

var registrations = map[string]registration{
	TypeStreamHello:       {Live, decodeAs[StreamHello]},
	TypeStreamReset:       {Live, decodeAs[StreamReset]},
	TypeStreamError:       {Live, decodeAs[StreamError]},
	TypeMessageUpserted:   {Durable, decodeAs[MessageUpserted]},
	TypeMessageRemoved:    {Durable, decodeAs[MessageRemoved]},
	TypePartUpserted:      {Durable, decodeAs[PartUpserted]},
	TypePartRemoved:       {Durable, decodeAs[PartRemoved]},
	TypePartDelta:         {Ephemeral, decodeAs[PartDelta]},
	TypeSessionStatus:     {Live, decodeAs[SessionStatusEvent]},
	TypeTurnStarted:       {Live, decodeAs[TurnStarted]},
	TypeTurnEnded:         {Live, decodeAs[TurnEnded]},
	TypeSessionUpdated:    {Durable, decodeAs[SessionUpdated]},
	TypeHistoryRewritten:  {Durable, decodeAs[HistoryRewritten]},
	TypeHistoryCommitted:  {Durable, decodeAs[HistoryCommitted]},
	TypePromptAsked:       {Live, decodeAs[PromptAsked]},
	TypePromptJudge:       {Live, decodeAs[PromptJudge]},
	TypePromptResolved:    {Live, decodeAs[PromptResolved]},
	TypeQueueChanged:      {Live, decodeAs[QueueChanged]},
	TypeBackgroundChanged: {Live, decodeAs[BackgroundChanged]},
	TypeChildUpserted:     {Durable, decodeAs[ChildUpserted]},
	TypeChildStatus:       {Live, decodeAs[ChildStatus]},
	TypeChangesDirty:      {Live, decodeAs[ChangesDirty]},
	TypeSessionError:      {Durable, decodeAs[SessionError]},
}

// Types lists every registered event type, sorted.
func Types() []string {
	out := make([]string, 0, len(registrations))
	for typ := range registrations {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}

// typed marshals v (the event's fields) with the type discriminator first.
func typed(ev Event, v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	head, _ := json.Marshal(ev.EventType())
	var b bytes.Buffer
	b.Grow(len(head) + len(body) + 9)
	b.WriteString(`{"type":`)
	b.Write(head)
	if len(body) > 2 {
		b.WriteByte(',')
	}
	b.Write(body[1:])
	return b.Bytes(), nil
}

func durabilityOf(typ string) Durability { return registrations[typ].durability }

func (StreamHello) EventType() string        { return TypeStreamHello }
func (StreamReset) EventType() string        { return TypeStreamReset }
func (StreamError) EventType() string        { return TypeStreamError }
func (MessageUpserted) EventType() string    { return TypeMessageUpserted }
func (MessageRemoved) EventType() string     { return TypeMessageRemoved }
func (PartUpserted) EventType() string       { return TypePartUpserted }
func (PartRemoved) EventType() string        { return TypePartRemoved }
func (PartDelta) EventType() string          { return TypePartDelta }
func (SessionStatusEvent) EventType() string { return TypeSessionStatus }
func (TurnStarted) EventType() string        { return TypeTurnStarted }
func (TurnEnded) EventType() string          { return TypeTurnEnded }
func (SessionUpdated) EventType() string     { return TypeSessionUpdated }
func (HistoryRewritten) EventType() string   { return TypeHistoryRewritten }
func (HistoryCommitted) EventType() string   { return TypeHistoryCommitted }
func (PromptAsked) EventType() string        { return TypePromptAsked }
func (PromptJudge) EventType() string        { return TypePromptJudge }
func (PromptResolved) EventType() string     { return TypePromptResolved }
func (QueueChanged) EventType() string       { return TypeQueueChanged }
func (BackgroundChanged) EventType() string  { return TypeBackgroundChanged }
func (ChildUpserted) EventType() string      { return TypeChildUpserted }
func (ChildStatus) EventType() string        { return TypeChildStatus }
func (ChangesDirty) EventType() string       { return TypeChangesDirty }
func (SessionError) EventType() string       { return TypeSessionError }

func (e StreamHello) durability() Durability        { return durabilityOf(e.EventType()) }
func (e StreamReset) durability() Durability        { return durabilityOf(e.EventType()) }
func (e StreamError) durability() Durability        { return durabilityOf(e.EventType()) }
func (e MessageUpserted) durability() Durability    { return durabilityOf(e.EventType()) }
func (e MessageRemoved) durability() Durability     { return durabilityOf(e.EventType()) }
func (e PartUpserted) durability() Durability       { return durabilityOf(e.EventType()) }
func (e PartRemoved) durability() Durability        { return durabilityOf(e.EventType()) }
func (e PartDelta) durability() Durability          { return durabilityOf(e.EventType()) }
func (e SessionStatusEvent) durability() Durability { return durabilityOf(e.EventType()) }
func (e TurnStarted) durability() Durability        { return durabilityOf(e.EventType()) }
func (e TurnEnded) durability() Durability          { return durabilityOf(e.EventType()) }
func (e SessionUpdated) durability() Durability     { return durabilityOf(e.EventType()) }
func (e HistoryRewritten) durability() Durability   { return durabilityOf(e.EventType()) }
func (e HistoryCommitted) durability() Durability   { return durabilityOf(e.EventType()) }
func (e PromptAsked) durability() Durability        { return durabilityOf(e.EventType()) }
func (e PromptJudge) durability() Durability        { return durabilityOf(e.EventType()) }
func (e PromptResolved) durability() Durability     { return durabilityOf(e.EventType()) }
func (e QueueChanged) durability() Durability       { return durabilityOf(e.EventType()) }
func (e BackgroundChanged) durability() Durability  { return durabilityOf(e.EventType()) }
func (e ChildUpserted) durability() Durability      { return durabilityOf(e.EventType()) }
func (e ChildStatus) durability() Durability        { return durabilityOf(e.EventType()) }
func (e ChangesDirty) durability() Durability       { return durabilityOf(e.EventType()) }
func (e SessionError) durability() Durability       { return durabilityOf(e.EventType()) }

// Each event marshals with its "type" so nested uses (the hello overlay)
// carry the discriminator too. The local types drop these methods to avoid
// recursion.
func (e StreamHello) MarshalJSON() ([]byte, error)     { type t StreamHello; return typed(e, t(e)) }
func (e StreamReset) MarshalJSON() ([]byte, error)     { type t StreamReset; return typed(e, t(e)) }
func (e StreamError) MarshalJSON() ([]byte, error)     { type t StreamError; return typed(e, t(e)) }
func (e MessageUpserted) MarshalJSON() ([]byte, error) { type t MessageUpserted; return typed(e, t(e)) }
func (e MessageRemoved) MarshalJSON() ([]byte, error)  { type t MessageRemoved; return typed(e, t(e)) }
func (e PartUpserted) MarshalJSON() ([]byte, error)    { type t PartUpserted; return typed(e, t(e)) }
func (e PartRemoved) MarshalJSON() ([]byte, error)     { type t PartRemoved; return typed(e, t(e)) }
func (e PartDelta) MarshalJSON() ([]byte, error)       { type t PartDelta; return typed(e, t(e)) }
func (e SessionStatusEvent) MarshalJSON() ([]byte, error) {
	type t SessionStatusEvent
	return typed(e, t(e))
}
func (e TurnStarted) MarshalJSON() ([]byte, error)    { type t TurnStarted; return typed(e, t(e)) }
func (e TurnEnded) MarshalJSON() ([]byte, error)      { type t TurnEnded; return typed(e, t(e)) }
func (e SessionUpdated) MarshalJSON() ([]byte, error) { type t SessionUpdated; return typed(e, t(e)) }
func (e HistoryRewritten) MarshalJSON() ([]byte, error) {
	type t HistoryRewritten
	return typed(e, t(e))
}
func (e HistoryCommitted) MarshalJSON() ([]byte, error) {
	type t HistoryCommitted
	return typed(e, t(e))
}
func (e PromptAsked) MarshalJSON() ([]byte, error)    { type t PromptAsked; return typed(e, t(e)) }
func (e PromptJudge) MarshalJSON() ([]byte, error)    { type t PromptJudge; return typed(e, t(e)) }
func (e PromptResolved) MarshalJSON() ([]byte, error) { type t PromptResolved; return typed(e, t(e)) }
func (e QueueChanged) MarshalJSON() ([]byte, error)   { type t QueueChanged; return typed(e, t(e)) }
func (e BackgroundChanged) MarshalJSON() ([]byte, error) {
	type t BackgroundChanged
	return typed(e, t(e))
}
func (e ChildUpserted) MarshalJSON() ([]byte, error) { type t ChildUpserted; return typed(e, t(e)) }
func (e ChildStatus) MarshalJSON() ([]byte, error)   { type t ChildStatus; return typed(e, t(e)) }
func (e ChangesDirty) MarshalJSON() ([]byte, error)  { type t ChangesDirty; return typed(e, t(e)) }
func (e SessionError) MarshalJSON() ([]byte, error)  { type t SessionError; return typed(e, t(e)) }
