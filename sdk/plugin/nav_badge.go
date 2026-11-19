package plugin

import (
	"encoding/json"

	"github.com/ocman/ocman/sdk/protocol"
)

type NavBadge = protocol.NavBadge

const (
	NavBadgeGrant    = protocol.NavBadgeGrant
	NavBadgeDot      = protocol.NavBadgeDot
	NavBadgeCount    = protocol.NavBadgeCount
	NavBadgeClear    = protocol.NavBadgeClear
	NavBadgeMaxCount = protocol.NavBadgeMaxCount
)

var NavBadgeCapability = protocol.NavBadgeCapability

// NewNavBadge builds a validated replacement for the plugin's navigation
// badge. Send it on the event channel passed to RunWithEvents or RunWithRequests.
func NewNavBadge(badge NavBadge) (Event, error) {
	if err := badge.Validate(); err != nil {
		return Event{}, err
	}
	data, err := json.Marshal(badge)
	if err != nil {
		return Event{}, err
	}
	return Event{Capability: NavBadgeCapability.Name, Name: protocol.NavBadgeSetEvent, Data: data}, nil
}
