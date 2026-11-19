package plugin

import (
	"encoding/json"

	"github.com/ocman/ocman/sdk/protocol"
)

type (
	Notice         = protocol.Notice
	NoticeAction   = protocol.NoticeAction
	NoticeSnapshot = protocol.NoticeSnapshot
)

const (
	NoticeGrant        = protocol.NoticeGrant
	NoticeToneInfo     = protocol.NoticeToneInfo
	NoticeToneWarning  = protocol.NoticeToneWarning
	NoticeToneCritical = protocol.NoticeToneCritical
	NoticeMaxNotices   = protocol.NoticeMaxNotices
	NoticeMaxActions   = protocol.NoticeMaxActions
	NoticeMaxTitle     = protocol.NoticeMaxTitle
	NoticeMaxBody      = protocol.NoticeMaxBody
	NoticeMaxStartedAt = protocol.NoticeMaxStartedAt
)

var NoticeCapability = protocol.NoticeCapability

// NewNotices builds a validated replacement for every active notice of the
// plugin installation. Call it with no notices to clear them. Send the event
// on the channel passed to RunWithEvents or RunWithRequests.
func NewNotices(notices ...Notice) (Event, error) {
	snapshot := NoticeSnapshot{Notices: append([]Notice{}, notices...)}
	if err := snapshot.Validate(); err != nil {
		return Event{}, err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return Event{}, err
	}
	event := Event{Capability: NoticeCapability.Name, Name: protocol.NoticeSetEvent, Data: data}
	// Report failure here rather than let the host keep stale notices.
	if _, err := protocol.DecodeNoticeSnapshot(event); err != nil {
		return Event{}, err
	}
	return event, nil
}
