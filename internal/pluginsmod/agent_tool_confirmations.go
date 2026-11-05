package pluginsmod

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sync"
	"time"

	plugins "github.com/ocman/ocman/sdk/protocol"
)

const (
	// toolConfirmationTTL bounds both an unanswered request and the use of an
	// approval. Decided records stay visible for the same time afterwards.
	toolConfirmationTTL        = 10 * time.Minute
	maxPendingToolConfirmation = 64
)

// Confirmation statuses as shown to the user.
const (
	confirmationPending  = "pending"
	confirmationApproved = "approved"
	confirmationDenied   = "denied"
	confirmationUsed     = "used"
	confirmationExpired  = "expired"
)

// toolConfirmation is one agent request awaiting the user's decision. It is
// held in memory only: a restart discards it, and so fails closed.
type toolConfirmation struct {
	ID        string          `json:"id"`
	PluginID  string          `json:"pluginId"`
	Tool      string          `json:"tool"`
	Key       string          `json:"key"`
	Text      string          `json:"text"`
	Arguments json.RawMessage `json:"arguments"`
	Status    string          `json:"status"`
	CreatedAt int64           `json:"createdAt"`
	ExpiresAt int64           `json:"expiresAt"`

	arguments   [32]byte
	description [32]byte
	process     *plugins.Process
}

type toolConfirmations struct {
	mu      sync.Mutex
	now     func() time.Time
	records map[string]*toolConfirmation
}

func digest(raw []byte) [32]byte { return sha256.Sum256(raw) }

func descriptionDigest(d plugins.Description) [32]byte {
	raw, _ := json.Marshal(d)
	return digest(raw)
}

func (c *toolConfirmations) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// expireLocked updates lapsed statuses and forgets records whose display
// period after their deadline has passed.
func (c *toolConfirmations) expireLocked(now time.Time) {
	for id, r := range c.records {
		expires := time.UnixMilli(r.ExpiresAt)
		if (r.Status == confirmationPending || r.Status == confirmationApproved) && !now.Before(expires) {
			r.Status = confirmationExpired
		}
		if now.After(expires.Add(toolConfirmationTTL)) {
			delete(c.records, id)
		}
	}
}

// request records a pending confirmation for exactly these arguments, bound
// to the current declaration and process.
func (c *toolConfirmations) request(pluginID, tool, key, text string, display json.RawMessage, args []byte, d plugins.Description, process *plugins.Process) (*toolConfirmation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	c.expireLocked(now)
	pending := 0
	for _, r := range c.records {
		if r.PluginID == pluginID && r.Status == confirmationPending {
			pending++
		}
	}
	if pending >= maxPendingToolConfirmation {
		return nil, plugins.ErrUnavailable
	}
	if c.records == nil {
		c.records = map[string]*toolConfirmation{}
	}
	r := &toolConfirmation{ID: "tc_" + rand.Text(), PluginID: pluginID, Tool: tool, Key: key, Text: text, Arguments: display,
		Status: confirmationPending, CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(toolConfirmationTTL).UnixMilli(),
		arguments: digest(args), description: descriptionDigest(d), process: process}
	c.records[r.ID] = r
	copied := *r
	return &copied, nil
}

// errConfirmation explains a refused resume to the agent in fixed text.
type errConfirmation string

func (e errConfirmation) Error() string { return string(e) }

// redeem consumes an approved confirmation for an identical call. A pending
// request is left untouched so the agent can retry after the user decides.
func (c *toolConfirmations) redeem(id, pluginID, tool string, args []byte, d plugins.Description, process *plugins.Process) (*plugins.ToolConfirmed, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireLocked(c.clock())
	r := c.records[id]
	switch {
	case r == nil || r.PluginID != pluginID || r.Tool != tool:
		return nil, errConfirmation("unknown confirmation_id for this tool; call without it to request a new confirmation")
	case r.arguments != digest(args):
		return nil, errConfirmation("confirmation_id was approved for different arguments; call again with the exact same arguments")
	case r.description != descriptionDigest(d) || r.process != process:
		r.Status = confirmationExpired
		return nil, errConfirmation("the plugin changed or restarted since this confirmation was requested; request a new confirmation")
	case r.Status == confirmationPending:
		return nil, errConfirmation("the user has not approved this request yet; ask the user to approve it in ocman, then retry")
	case r.Status != confirmationApproved:
		return nil, errConfirmation("this confirmation was " + r.Status + "; it cannot be used")
	}
	r.Status = confirmationUsed
	return &plugins.ToolConfirmed{ID: r.ID, Key: r.Key, Text: r.Text}, nil
}

// decide records the user's decision on a pending request. allowed rechecks
// the plugin's current authorization for the tool.
func (c *toolConfirmations) decide(pluginID, id string, approve bool, allowed func(*toolConfirmation) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	c.expireLocked(now)
	r := c.records[id]
	if r == nil || r.PluginID != pluginID {
		return plugins.ErrInvalidMessage
	}
	if r.Status != confirmationPending {
		return errPluginConflict
	}
	if !approve {
		r.Status = confirmationDenied
		return nil
	}
	if err := allowed(r); err != nil {
		return err
	}
	r.Status = confirmationApproved
	r.ExpiresAt = now.Add(toolConfirmationTTL).UnixMilli()
	return nil
}

// list returns a plugin's visible records, newest first.
func (c *toolConfirmations) list(pluginID string) []toolConfirmation {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expireLocked(c.clock())
	out := []toolConfirmation{}
	for _, r := range c.records {
		if r.PluginID == pluginID {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b toolConfirmation) int {
		return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// revoke expires a plugin's open requests, for disable, removal and grant changes.
func (c *toolConfirmations) revoke(pluginID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.PluginID == pluginID && (r.Status == confirmationPending || r.Status == confirmationApproved) {
			r.Status = confirmationExpired
		}
	}
}
