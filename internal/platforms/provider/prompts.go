package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"

	"github.com/ocman/ocman/internal/platforms"
)

// ErrReconciliationConflict means the response was superseded by a local prompt
// mutation. In particular, callers must not treat absence as authoritative.
var ErrReconciliationConflict = errors.New("provider prompts changed during reconciliation")

// Copy through JSON so callers cannot mutate nested prompt metadata in the cache.
func clonePrompt(prompt platforms.LivePrompt) platforms.LivePrompt {
	data, _ := json.Marshal(prompt)
	var copy platforms.LivePrompt
	_ = json.Unmarshal(data, &copy)
	return copy
}

func (a *Adapter) prompts(supported bool, id string, questions bool) ([]platforms.LivePrompt, error) {
	if !supported {
		return nil, platforms.ErrUnsupported
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.promptsLocked(id, questions), nil
}

func (a *Adapter) promptsLocked(id string, questions bool) []platforms.LivePrompt {
	cache := a.permissions
	if questions {
		cache = a.questions
	}
	result := []platforms.LivePrompt{}
	for _, prompt := range cache {
		if prompt["sessionID"] == id {
			result = append(result, clonePrompt(prompt))
		}
	}
	sort.Slice(result, func(i, j int) bool { return fmt.Sprint(result[i]["id"]) < fmt.Sprint(result[j]["id"]) })
	return result
}

// ListPermissions reads the event-fed cache (a draining adapter's too).
func (a *Adapter) ListPermissions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	return a.prompts(a.Capabilities().RespondPermission, id, false)
}
func (a *Adapter) ListQuestions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	return a.prompts(a.Capabilities().RespondQuestion, id, true)
}

func (a *Adapter) refresh(ctx context.Context, id string, questions bool) ([]platforms.LivePrompt, error) {
	path, supported := "/permissions", a.Capabilities().RespondPermission
	if questions {
		path, supported = "/questions", a.Capabilities().RespondQuestion
	}
	// Never block event delivery or cancellation on a network read. If a prompt
	// changed during reconciliation, absence in that snapshot is not authoritative.
	a.mu.RLock()
	version := a.promptVersion
	a.mu.RUnlock()
	var prompts []platforms.LivePrompt
	if err := a.call(ctx, supported, "GET", path+"?"+url.Values{"sessionID": {id}}.Encode(), nil, &prompts); err != nil {
		return nil, err
	}
	for _, p := range prompts {
		a.exactPrompt(p)
		pid, _ := p["id"].(string)
		if pid == "" || p["sessionID"] != id {
			return nil, fmt.Errorf("provider returned invalid pending prompt")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if version != a.promptVersion {
		return nil, ErrReconciliationConflict
	}
	cache := a.permissions
	if questions {
		cache = a.questions
	}
	kept := make([]platforms.LivePrompt, 0, len(prompts))
	for _, p := range prompts {
		pid := p["id"].(string)
		// The supervisor still lists a prompt ocman's rules are answering;
		// one the stream has not delivered yet is left to that event, which
		// applies the rules first. Neither may reach the judge or a browser.
		if !questions && a.rules != nil {
			if _, cached := cache[pid]; !cached && (a.ruled[pid] || a.rules.Decide(p) != "") {
				continue
			}
		}
		kept = append(kept, p)
	}
	for pid, p := range cache {
		if p["sessionID"] == id {
			delete(cache, pid)
		}
	}
	for _, p := range kept {
		cache[p["id"].(string)] = clonePrompt(p)
	}
	a.promptVersion++
	return kept, nil
}
func (a *Adapter) RefreshPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	return a.refresh(ctx, id, false)
}
func (a *Adapter) RefreshQuestions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	return a.refresh(ctx, id, true)
}
