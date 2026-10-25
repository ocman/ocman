package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	log "github.com/sirupsen/logrus"
)

// Rules is the ocman-owned permission store for a provider whose supervisor
// must never hold a ruleset (AD-7a). It keeps each session's ruleset (YOLO,
// custom modes and inherited rules) and, separately, the session's "Allow
// always" replies, which OpenCode also keeps outside the visible ruleset.
type Rules struct {
	mu   sync.Mutex
	path string
	data struct {
		Rules    map[string][]platforms.PermissionRule `json:"rules"`
		Approved map[string][]platforms.PermissionRule `json:"approved"`
		// Tentative are approvals saved before their "always" reply was
		// sent, by permission id. They grant nothing until the reply's
		// outcome is known: an effective "always" moves them to Approved,
		// any other outcome drops them.
		Tentative map[string]tentative `json:"tentative,omitempty"`
		// Retain format markers so unsupported raw approvals fail closed.
		ExactPatterns bool `json:"exactPatterns"`
		ExactRules    bool `json:"exactRules"`
	}
}

type tentative struct {
	Session string                     `json:"session"`
	Rules   []platforms.PermissionRule `json:"rules"`
}

// OpenRules loads path; an empty path keeps the rules in memory. A store
// that exists but cannot be read fails: starting empty would silently drop
// every deny rule.
func OpenRules(path string) (*Rules, error) {
	r := &Rules{path: path}
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &r.data); err != nil {
				return nil, fmt.Errorf("permission rules %s: %w", path, err)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}
	r.initLocked()
	if !r.data.ExactPatterns && len(r.data.Approved)+len(r.data.Tentative) > 0 || !r.data.ExactRules && len(r.data.Rules) > 0 {
		return nil, fmt.Errorf("permission rules %s use an unsupported pre-release format", path)
	}
	r.data.ExactPatterns, r.data.ExactRules = true, true
	return r, nil
}

// commitLocked persists the store after mutate and undoes mutate when the
// write fails, so memory never answers with a rule a restart would lose.
func (r *Rules) commitLocked(mutate func()) error {
	before, _ := json.Marshal(r.data)
	mutate()
	if r.path == "" {
		return nil
	}
	data, _ := json.Marshal(r.data)
	tmp := r.path + ".tmp"
	err := os.MkdirAll(filepath.Dir(tmp), 0o700)
	if err == nil {
		err = os.WriteFile(tmp, data, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, r.path)
	}
	if err != nil {
		r.data.Rules, r.data.Approved, r.data.Tentative = nil, nil, nil
		_ = json.Unmarshal(before, &r.data)
		r.initLocked()
		return fmt.Errorf("saving permission rules: %w", err)
	}
	return nil
}

func (r *Rules) initLocked() {
	if r.data.Rules == nil {
		r.data.Rules = map[string][]platforms.PermissionRule{}
	}
	if r.data.Approved == nil {
		r.data.Approved = map[string][]platforms.PermissionRule{}
	}
	if r.data.Tentative == nil {
		r.data.Tentative = map[string]tentative{}
	}
}

func (r *Rules) get(id string) []platforms.PermissionRule {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]platforms.PermissionRule{}, r.data.Rules[id]...)
}

func (r *Rules) set(id string, rules []platforms.PermissionRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commitLocked(func() {
		if len(rules) == 0 {
			delete(r.data.Rules, id) // empty restores the defaults: ask
		} else {
			r.data.Rules[id] = append([]platforms.PermissionRule(nil), rules...)
		}
	})
}

// approveTentatively saves rules for permission pid's "always" reply before
// it is sent. They grant nothing until settle confirms the reply claude
// received was "always".
func (r *Rules) approveTentatively(id, pid string, rules []platforms.PermissionRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commitLocked(func() {
		t := r.data.Tentative[pid]
		t.Session = id
		for _, rule := range rules {
			if !slices.Contains(t.Rules, rule) {
				t.Rules = append(t.Rules, rule)
			}
		}
		r.data.Tentative[pid] = t
	})
}

// settle resolves pid's tentative approval: granted when the reply claude
// received was "always", dropped otherwise. Unknown ids are a no-op.
func (r *Rules) settle(pid string, always bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.data.Tentative[pid]
	if !ok {
		return nil
	}
	return r.commitLocked(func() {
		delete(r.data.Tentative, pid)
		for _, rule := range t.Rules {
			if always && !slices.Contains(r.data.Approved[t.Session], rule) {
				r.data.Approved[t.Session] = append(r.data.Approved[t.Session], rule)
			}
		}
	})
}

// Decide evaluates a prompt like OpenCode: per pattern the last matching rule
// wins, any deny denies, any ask asks, and only all-allow allows. It returns
// "" when the judge or the user must decide.
func (r *Rules) Decide(prompt platforms.LivePrompt) string {
	id, _ := prompt["sessionID"].(string)
	permission, _ := prompt["permission"].(string)
	r.mu.Lock()
	rules := append(append([]platforms.PermissionRule{}, r.data.Rules[id]...), r.data.Approved[id]...)
	configured := len(r.data.Rules[id])
	r.mu.Unlock()
	patterns := prompt.StringsField("patterns")
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	result := "allow"
	metadata, _ := prompt["metadata"].(map[string]any)
	preflight, _ := metadata["ocmanPreflight"].(bool)
	for _, pattern := range patterns {
		// A prompt's patterns are exact patterns: match the call itself.
		value := providerprotocol.PatternValue(pattern)
		action := "ask"
		if preflight {
			action = "allow"
		} // no ocman restriction: continue to native checks
		for i, rule := range rules {
			if i == configured && action == "deny" {
				break // saved approvals may satisfy ask rules, never configured denials
			}
			if providerprotocol.MatchPattern(rule.Permission, permission) && providerprotocol.MatchPattern(rule.Pattern, value) {
				action = rule.Action
			}
		}
		switch {
		case action == "deny":
			return "reject"
		case action != "allow":
			result = ""
		}
	}
	if result == "allow" {
		return "once"
	}
	return ""
}

// SetRules makes the adapter answer rule-decided prompts itself and serve
// the session rulesets locally instead of through the provider's rules RPC.
// Call before WatchEvents and before any browser stream.
func (a *Adapter) SetRules(r *Rules) {
	a.mu.Lock()
	a.rules = r
	a.mu.Unlock()
}

func (a *Adapter) localRules() *Rules {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rules
}

// ruleDecision is the reply ocman's rules give an asked permission, or "".
// Rule-decided prompts never reach the pending cache, the judge or a browser.
// The headless consumer's decision is authoritative: a prompt it answered by
// rule stays hidden, and one it left pending for the judge or user stays
// visible even if the rules changed since.
func (a *Adapter) ruleDecision(event providerprotocol.Event) (platforms.LivePrompt, string) {
	r := a.localRules()
	if event.Type != "permission.asked" {
		return nil, ""
	}
	var prompt platforms.LivePrompt
	if json.Unmarshal(event.Properties, &prompt) != nil {
		return nil, ""
	}
	if r == nil {
		metadata, _ := prompt["metadata"].(map[string]any)
		if metadata["ocmanPreflight"] == true {
			return prompt, "reject"
		}
		return nil, ""
	}
	id, _ := prompt["id"].(string)
	a.mu.RLock()
	ruled, pending := a.ruled[id], a.permissions[id] != nil
	a.mu.RUnlock()
	switch {
	case ruled:
		return prompt, "ruled"
	case pending:
		return prompt, ""
	}
	return prompt, r.Decide(prompt)
}

// ruleReplyBackoff bounds the retry delay of a failed rule reply.
var ruleReplyBackoff = [2]time.Duration{250 * time.Millisecond, 30 * time.Second}

// answerByRule replies outside the event loop: the stream keeps flowing and
// the provider's permission.replied clears every consumer. A failed reply is
// logged and retried with backoff until it is delivered, the provider no
// longer holds the prompt, or the adapter closes; the prompt stays pending
// in the provider meanwhile and is never handed to the judge or a browser.
// An ocman restart replays the unacknowledged ask and the rules answer again.
func (a *Adapter) answerByRule(prompt platforms.LivePrompt, reply string) {
	id, _ := prompt["id"].(string)
	sessionID, _ := prompt["sessionID"].(string)
	go func() {
		delay := ruleReplyBackoff[0]
		for attempt := 1; ; attempt++ {
			err := a.call(a.ctx, true, "POST", "/permissions/"+url.PathEscape(id)+"/reply",
				platforms.RespondPermissionRequest{SessionID: sessionID, PermissionID: id, Reply: reply}, nil)
			if err == nil || errors.Is(err, platforms.ErrNotFound) || a.ctx.Err() != nil {
				return
			}
			a.mu.RLock()
			_, pending := a.ruled[id]
			a.mu.RUnlock()
			if !pending { // the provider already reported the reply
				return
			}
			log.WithError(err).WithFields(log.Fields{"platform": a.ID(), "sessionID": sessionID, "permissionID": id, "attempt": attempt}).
				Warn("provider: delivering a rule-decided permission reply failed; retrying")
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(delay):
			}
			delay = min(delay*2, ruleReplyBackoff[1])
		}
	}()
}

// rememberAlways tentatively adds an "Allow always" reply to the session's
// approvals, scoped to the prompt's always patterns (its patterns when
// absent), before the reply is sent; settleAlways resolves it from the
// outcome.
func (a *Adapter) rememberAlways(prompt platforms.LivePrompt) error {
	r := a.localRules()
	if r == nil || prompt == nil {
		return nil
	}
	id, _ := prompt["sessionID"].(string)
	permission, _ := prompt["permission"].(string)
	patterns := prompt.StringsField("always")
	if len(patterns) == 0 {
		patterns = prompt.StringsField("patterns")
	}
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	rules := make([]platforms.PermissionRule, 0, len(patterns))
	for _, p := range patterns {
		rules = append(rules, platforms.PermissionRule{Permission: permission, Pattern: p, Action: "allow"})
	}
	pid, _ := prompt["id"].(string)
	return r.approveTentatively(id, pid, rules)
}

// settleAlways grants or drops permission pid's tentative approval once the
// reply claude received is known: a successful "always" reply, or the
// provider's permission.replied. A store that cannot record it leaves the
// approval tentative (granting nothing): the headless stream stops before
// that event, so it is replayed, and its turn left unacknowledged, until
// the store records the outcome.
func (a *Adapter) settleAlways(pid string, always bool) error {
	r := a.localRules()
	if r == nil {
		return nil
	}
	if err := r.settle(pid, always); err != nil {
		log.WithError(err).WithFields(log.Fields{"platform": a.ID(), "permissionID": pid}).Warn("provider: settling an Allow-always approval")
		return err
	}
	return nil
}
