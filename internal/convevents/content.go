package convevents

import (
	"unicode/utf16"

	"github.com/ocman/ocman/internal/db"
)

type MessageUpserted struct {
	Message  db.Message `json:"message"`
	Position *int64     `json:"position,omitempty"`
}

// MessageRemoved reasons.
const (
	RemovedDeleted    = "deleted"
	RemovedReverted   = "reverted"
	RemovedSuperseded = "superseded"
)

type MessageRemoved struct {
	MessageID string `json:"messageId"`
	Reason    string `json:"reason"`
}

type PartUpserted struct {
	Part Part `json:"part"`
	// Final is true once the part can no longer grow. Final upserts replace
	// local streaming fields unconditionally.
	Final bool `json:"final"`
}

// Part is Ocman's part schema: the REST part, except Data is always a JSON
// object shaped like PartData.
type Part struct {
	db.Part
	// Position is the source-defined order inside the message when known.
	Position *int64 `json:"position,omitempty"`
}

type PartRemoved struct {
	MessageID string `json:"messageId"`
	PartID    string `json:"partId"`
}

// Delta fields and the PartData path each one appends to.
const (
	FieldText       = "text"       // data.text
	FieldToolInput  = "tool.input" // data.state.raw
	FieldToolOutput = "tool.output"
)

type PartDelta struct {
	MessageID string `json:"messageId"`
	PartID    string `json:"partId"`
	Field     string `json:"field"`
	// Offset is the UTF-16 code unit offset of Text within the field, so it
	// matches String.prototype.length in the browser.
	Offset   int    `json:"offset"`
	Text     string `json:"text"`
	PartType string `json:"partType,omitempty"`
}

// UTF16Len is len(s) in UTF-16 code units. Invalid UTF-8 bytes count as one
// unit each, like the U+FFFD a browser decodes them to.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
