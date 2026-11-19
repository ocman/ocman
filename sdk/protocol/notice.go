package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// NoticeCapability is notice.v1, a plugin-to-host snapshot of one
// installation's currently active toast notices.
var NoticeCapability = Capability{Name: "notice", Version: Version{Major: 1}}

const (
	NoticeGrant    = "notice.toast"
	NoticeSetEvent = "set"

	NoticeToneInfo     = "info"
	NoticeToneWarning  = "warning"
	NoticeToneCritical = "critical"

	// NoticeMaxNotices bounds one installation's simultaneous notices.
	NoticeMaxNotices = 3
	// NoticeMaxActions bounds the declared actions one notice may offer.
	NoticeMaxActions = 2
	// NoticeMaxTitle and NoticeMaxBody are limits in characters (runes).
	NoticeMaxTitle = 80
	NoticeMaxBody  = 240
	// noticeMaxData bounds the encoded snapshot. The other limits keep any
	// valid snapshot well below it.
	noticeMaxData = 4096
)

// NoticeAction references an action.v1 action the plugin declared. The host
// renders the declared label and runs it through the action broker, including
// its confirmation and operation-ID deduplication.
type NoticeAction struct {
	ActionID string `json:"actionId"`
}

// Notice is one persistent toast. Title and Body are plain text that the host
// renders as text; they never contain HTML, markdown links or URLs.
type Notice struct {
	ID    string `json:"id"`
	Tone  string `json:"tone"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// StartedAt is an optional Unix time in milliseconds. The host renders the
	// elapsed time since then.
	StartedAt int64          `json:"startedAt,omitempty"`
	Actions   []NoticeAction `json:"actions,omitempty"`
}

// NoticeSnapshot replaces every notice the installation published before.
// An empty Notices list clears them.
type NoticeSnapshot struct {
	Notices []Notice `json:"notices"`
}

func noticeText(s string, limit int) bool {
	return s != "" && utf8.RuneCountInString(s) <= limit && utf8.ValidString(s) && strings.TrimSpace(s) == s &&
		!strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }) &&
		!strings.ContainsAny(s, "<>") && !strings.Contains(s, "://")
}

func (n Notice) Validate() error {
	if !validName(n.ID) || len(n.ID) > 64 || !slices.Contains([]string{NoticeToneInfo, NoticeToneWarning, NoticeToneCritical}, n.Tone) ||
		!noticeText(n.Title, NoticeMaxTitle) || (n.Body != "" && !noticeText(n.Body, NoticeMaxBody)) || n.StartedAt < 0 || len(n.Actions) > NoticeMaxActions {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, a := range n.Actions {
		if !validName(a.ActionID) || seen[a.ActionID] {
			return ErrInvalidMessage
		}
		seen[a.ActionID] = true
	}
	return nil
}

func (s NoticeSnapshot) Validate() error {
	if s.Notices == nil || len(s.Notices) > NoticeMaxNotices {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, n := range s.Notices {
		if n.Validate() != nil || seen[n.ID] {
			return ErrInvalidMessage
		}
		seen[n.ID] = true
	}
	return nil
}

// DecodeNoticeSnapshot accepts only the closed notice schema. Field names are
// matched exactly, optional fields must be omitted rather than empty, and
// unknown fields such as URLs, routes or owner selectors are rejected.
func DecodeNoticeSnapshot(event Event) (NoticeSnapshot, error) {
	if event.Capability != NoticeCapability.Name || event.Name != NoticeSetEvent ||
		len(event.Data) > noticeMaxData || checkJSON(event.Data) != nil {
		return NoticeSnapshot{}, ErrInvalidMessage
	}
	var snapshot NoticeSnapshot
	var raw any
	if json.Unmarshal(event.Data, &snapshot) != nil || json.Unmarshal(event.Data, &raw) != nil {
		return NoticeSnapshot{}, ErrInvalidMessage
	}
	// encoding/json matches keys case-insensitively and ignores unknown ones.
	// Re-encoding the typed value must reproduce the input exactly.
	canonical, err := json.Marshal(snapshot)
	var round any
	if err != nil || json.Unmarshal(canonical, &round) != nil || !reflect.DeepEqual(raw, round) {
		return NoticeSnapshot{}, ErrInvalidMessage
	}
	return snapshot, snapshot.Validate()
}

func (d Description) offersNotice() bool {
	return slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
		return c.Name == NoticeCapability.Name && c.Version.Major == NoticeCapability.Version.Major
	})
}

// NoticeAllowed checks both the opt-in and the owner's approved grant.
func (d Description) NoticeAllowed(grants []string) bool {
	return d.offersNotice() && slices.Contains(d.RequestedGrants, NoticeGrant) && slices.Contains(grants, NoticeGrant)
}

// Notices reads the current child connection's notices, if any. The returned
// slice is a copy.
func (p *Process) Notices() []Notice {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil || p.health.Status != "ready" || len(p.notices) == 0 {
		return nil
	}
	out := make([]Notice, len(p.notices))
	for i, n := range p.notices {
		n.Actions = slices.Clone(n.Actions)
		out[i] = n
	}
	return out
}

func (p *Process) setNotices(notices []Notice) {
	p.mu.Lock()
	p.notices = notices
	p.mu.Unlock()
}
