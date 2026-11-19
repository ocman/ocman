package protocol

import (
	"encoding/json"
	"slices"
)

// NavBadgeCapability is nav-badge.v1, a plugin-to-host snapshot of one
// installation's navigation badge.
var NavBadgeCapability = Capability{Name: "nav-badge", Version: Version{Major: 1}}

const (
	NavBadgeGrant    = "nav.badge"
	NavBadgeSetEvent = "set"
	NavBadgeDot      = "dot"
	NavBadgeCount    = "count"
	NavBadgeClear    = "clear"
	NavBadgeMaxCount = 999999
)

// NavBadge replaces the previous badge. Count is only used for kind "count".
type NavBadge struct {
	Kind  string `json:"kind"`
	Count int    `json:"count,omitempty"`
}

func (b NavBadge) Validate() error {
	switch b.Kind {
	case NavBadgeDot, NavBadgeClear:
		if b.Count == 0 {
			return nil
		}
	case NavBadgeCount:
		if b.Count > 0 && b.Count <= NavBadgeMaxCount {
			return nil
		}
	}
	return ErrInvalidMessage
}

// DecodeNavBadge rejects anything except a badge kind and, for a numeric
// badge, an integer count. It never accepts a target ID or free-form data.
func DecodeNavBadge(event Event) (NavBadge, error) {
	if event.Capability != NavBadgeCapability.Name || event.Name != NavBadgeSetEvent ||
		len(event.Data) > 128 || checkJSON(event.Data) != nil {
		return NavBadge{}, ErrInvalidMessage
	}
	var fields map[string]json.RawMessage
	var badge NavBadge
	if json.Unmarshal(event.Data, &fields) != nil || json.Unmarshal(fields["kind"], &badge.Kind) != nil {
		return NavBadge{}, ErrInvalidMessage
	}
	if badge.Kind == NavBadgeCount {
		if len(fields) != 2 || json.Unmarshal(fields["count"], &badge.Count) != nil {
			return NavBadge{}, ErrInvalidMessage
		}
	} else if len(fields) != 1 {
		return NavBadge{}, ErrInvalidMessage
	}
	return badge, badge.Validate()
}

func (d Description) offersNavBadge() bool {
	return slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
		return c.Name == NavBadgeCapability.Name && c.Version.Major == NavBadgeCapability.Version.Major
	})
}

// NavBadgeAllowed checks both the opt-in and the owner's approved grant.
func (d Description) NavBadgeAllowed(grants []string) bool {
	return d.offersNavBadge() && slices.Contains(d.RequestedGrants, NavBadgeGrant) && slices.Contains(grants, NavBadgeGrant)
}

// NavigationBadge reads the current child connection's badge, if any.
func (p *Process) NavigationBadge() (NavBadge, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil || p.health.Status != "ready" || p.navBadge.Kind == "" {
		return NavBadge{}, false
	}
	return p.navBadge, true
}

func (p *Process) setNavBadge(badge NavBadge) {
	if badge.Kind == NavBadgeClear {
		badge = NavBadge{}
	}
	p.mu.Lock()
	p.navBadge = badge
	p.mu.Unlock()
}
