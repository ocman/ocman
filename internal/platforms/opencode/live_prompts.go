package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ObservePromptAskedFromPort upserts one permission or question observed on
// the process-wide event stream. It ignores events from a stream invalidated by
// process removal while its final buffered event was still dispatching.
func (a *Adapter) ObservePromptAskedFromPort(port string, generation uint64, directory, kind string, prompt platforms.LivePrompt) {
	a.observePromptAsked(port, generation, directory, kind, prompt)
}

func (a *Adapter) observePromptAsked(port string, generation uint64, directory, kind string, prompt platforms.LivePrompt) {
	if a == nil || a.prompts == nil {
		return
	}
	sessionID := prompt.StringField("sessionID")
	requestID := prompt.StringField("id")
	if sessionID == "" || requestID == "" {
		return
	}
	a.prompts.mu.Lock()
	if generation != 0 && a.prompts.portGen[port] != generation {
		a.prompts.mu.Unlock()
		return
	}
	key := promptKey(kind, sessionID, requestID)
	a.prompts.recordHalt(kind, prompt)
	a.prompts.entries[key] = livePromptEntry{directory: directory, prompt: clonePrompt(prompt)}
	scope := promptScope(directory, kind)
	a.prompts.version[scope]++
	a.prompts.changed[key] = a.prompts.version[scope]
	a.prompts.changeScope[key] = scope
	if port != "" {
		a.prompts.scopePort[scope] = port
	}
	a.prompts.mu.Unlock()
}

func (a *Adapter) PromptPortGeneration(port string) uint64 {
	if a == nil || a.prompts == nil {
		return 0
	}
	a.prompts.mu.Lock()
	defer a.prompts.mu.Unlock()
	if a.prompts.portGen[port] == 0 {
		a.prompts.portGen[port] = 1
	}
	return a.prompts.portGen[port]
}

// ObservePromptResolved removes one terminal permission or question.
func (a *Adapter) ObservePromptResolved(directory, kind, sessionID, requestID string) {
	if a == nil || a.prompts == nil || sessionID == "" || requestID == "" {
		return
	}
	a.prompts.mu.Lock()
	key := promptKey(kind, sessionID, requestID)
	if directory == "" {
		directory = a.prompts.entries[key].directory
	}
	delete(a.prompts.entries, key)
	scope := promptScope(directory, kind)
	a.prompts.version[scope]++
	a.prompts.changed[key] = a.prompts.version[scope]
	a.prompts.changeScope[key] = scope
	a.prompts.mu.Unlock()
}

// StartPromptReconciliation fetches directory-scoped prompt snapshots in
// bounded parallelism. Scope versions are captured before the goroutine
// starts so events received after the global stream connects always win.
func (a *Adapter) StartPromptReconciliation(ctx context.Context, port string, directories []string, onPermission ...func(platforms.LivePrompt)) <-chan bool {
	return a.startPromptReconciliation(ctx, port, directories, []string{"permission", "question"}, true, onPermission...)
}

func (a *Adapter) startPromptReconciliation(ctx context.Context, port string, directories, kinds []string, expandDirectories bool, onPermission ...func(platforms.LivePrompt)) <-chan bool {
	done := make(chan bool, 1)
	if expandDirectories {
		directories = a.promptDirectories(port, directories)
	}
	tokens := make(map[string]uint64, len(directories)*len(kinds))
	a.prompts.mu.Lock()
	for _, directory := range directories {
		for _, kind := range kinds {
			scope := promptScope(directory, kind)
			a.prompts.version[scope]++
			a.prompts.scopePort[scope] = port
			tokens[scope] = a.prompts.version[scope]
		}
	}
	a.prompts.mu.Unlock()

	go func() {
		var allOK atomic.Bool
		allOK.Store(true)
		defer func() {
			done <- allOK.Load()
			close(done)
		}()
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for scope, token := range tokens {
			directory, kind, _ := strings.Cut(scope, "\x00")
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				prompts, ok := fetchPromptSnapshotWithRetry(ctx, port, directory, kind)
				var callback func(platforms.LivePrompt)
				if kind == "permission" && len(onPermission) > 0 {
					callback = onPermission[0]
				}
				if ok {
					a.prompts.applySnapshot(directory, kind, token, prompts, callback)
				} else {
					allOK.Store(false)
				}
			}()
		}
		wg.Wait()
	}()
	return done
}

// ClearPromptsForPort removes prompt state owned by a process that is no
// longer discoverable. Transient stream reconnects keep their state until
// reconciliation completes, avoiding UI flicker.
func (a *Adapter) ClearPromptsForPort(port string) {
	if a == nil || a.prompts == nil || port == "" {
		return
	}
	a.rt.forgetSessionsForPort(port)
	catalogCache.invalidatePort(port)
	fetchFailureLogs.invalidatePort(port)
	a.prompts.mu.Lock()
	a.prompts.portGen[port]++
	directories := make(map[string]bool)
	for scope, owner := range a.prompts.scopePort {
		if owner != port {
			continue
		}
		directory, _, _ := strings.Cut(scope, "\x00")
		directories[directory] = true
		delete(a.prompts.scopePort, scope)
		a.prompts.version[scope]++
		a.prompts.applied[scope] = a.prompts.version[scope]
	}
	for key, entry := range a.prompts.entries {
		if directories[entry.directory] {
			delete(a.prompts.entries, key)
			delete(a.prompts.changed, key)
			delete(a.prompts.changeScope, key)
		}
	}
	for key, scope := range a.prompts.changeScope {
		directory, _, _ := strings.Cut(scope, "\x00")
		if directories[directory] {
			delete(a.prompts.changed, key)
			delete(a.prompts.changeScope, key)
		}
	}
	a.prompts.mu.Unlock()
}

func (a *Adapter) promptDirectories(port string, discovered []string) []string {
	seen := make(map[string]bool, len(discovered))
	for _, directory := range discovered {
		seen[normalizePortDirectory(directory)] = true
	}
	out := append([]string(nil), discovered...)
	for _, directory := range a.prompts.directoriesForPort(port) {
		if !seen[normalizePortDirectory(directory)] {
			seen[normalizePortDirectory(directory)] = true
			out = append(out, directory)
		}
	}
	if a.db == nil {
		return out
	}
	// Worktree sessions run on their project's instance, so a worktree whose
	// turn may be open needs its own scope: one whose stored turn is
	// unfinished, or whose turn this port's event stream reports running.
	candidates, err := a.db.UnfinishedTurnDirectories(context.Background())
	if err != nil {
		return out
	}
	for _, id := range a.turns.runningOnPort(port) {
		if row, err := a.db.GetSession(context.Background(), id); err == nil && row != nil {
			candidates = append(candidates, row.Directory)
		}
	}
	for _, directory := range candidates {
		root := normalizePortDirectory(foldWorktreeToProjectRoot(directory))
		if !seen[root] || seen[normalizePortDirectory(directory)] {
			continue
		}
		seen[normalizePortDirectory(directory)] = true
		out = append(out, directory)
	}
	return out
}

func (r *livePromptRegistry) directoriesForPort(port string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]bool)
	var out []string
	for scope, owner := range r.scopePort {
		if owner != port {
			continue
		}
		directory, _, _ := strings.Cut(scope, "\x00")
		if !seen[directory] {
			seen[directory] = true
			out = append(out, directory)
		}
	}
	return out
}

func fetchPromptSnapshotWithRetry(ctx context.Context, port, directory, kind string) ([]platforms.LivePrompt, bool) {
	for attempt := 0; attempt < 3; attempt++ {
		if prompts, ok := fetchPromptSnapshot(ctx, port, directory, kind); ok {
			return prompts, true
		}
		if attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil, false
}

func fetchPromptSnapshot(ctx context.Context, port, directory, kind string) ([]platforms.LivePrompt, bool) {
	requestCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	endpoint := fmt.Sprintf("http://127.0.0.1:%s/%s?directory=%s", port, kind, url.QueryEscape(directory))
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	resp, err := openCodeClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return nil, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var prompts []platforms.LivePrompt
	if err := json.Unmarshal(body, &prompts); err != nil {
		return nil, false
	}
	return prompts, true
}

func (r *livePromptRegistry) applySnapshot(directory, kind string, token uint64, prompts []platforms.LivePrompt, onPrompt func(platforms.LivePrompt)) bool {
	r.mu.Lock()
	scope := promptScope(directory, kind)
	if token < r.applied[scope] {
		r.mu.Unlock()
		return false
	}
	r.applied[scope] = token
	for key, entry := range r.entries {
		if entry.directory == directory && strings.HasPrefix(key, kind+"\x00") && r.changed[key] <= token {
			delete(r.entries, key)
		}
	}
	var callbacks []platforms.LivePrompt
	for _, prompt := range prompts {
		sessionID := prompt.StringField("sessionID")
		requestID := prompt.StringField("id")
		key := promptKey(kind, sessionID, requestID)
		if sessionID != "" && requestID != "" && r.changed[key] <= token {
			r.recordHalt(kind, prompt)
			cloned := clonePrompt(prompt)
			r.entries[key] = livePromptEntry{directory: directory, prompt: cloned}
			if onPrompt != nil {
				callbacks = append(callbacks, clonePrompt(cloned))
			}
		}
	}
	for key, changed := range r.changed {
		if r.changeScope[key] == scope && changed <= token {
			delete(r.changed, key)
			delete(r.changeScope, key)
		}
	}
	r.mu.Unlock()
	// Observers may read session summaries, which consult this registry.
	for _, prompt := range callbacks {
		onPrompt(prompt)
	}
	return true
}

func (r *livePromptRegistry) find(kind, requestID string) (livePromptEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for key, entry := range r.entries {
		if strings.HasPrefix(key, kind+"\x00") && entry.prompt.StringField("id") == requestID {
			return livePromptEntry{directory: entry.directory, prompt: clonePrompt(entry.prompt)}, true
		}
	}
	return livePromptEntry{}, false
}

func (r *livePromptRegistry) pendingSessionIDs() (map[string]bool, map[string]bool) {
	permissions := make(map[string]bool)
	questions := make(map[string]bool)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for key, entry := range r.entries {
		sessionID := entry.prompt.StringField("sessionID")
		if len(key) >= len("permission") && key[:len("permission")] == "permission" {
			permissions[sessionID] = true
		} else {
			questions[sessionID] = true
		}
	}
	return permissions, questions
}

func (r *livePromptRegistry) listEntries(kind string) []livePromptEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []livePromptEntry
	for key, entry := range r.entries {
		if key[:len(kind)+1] == kind+"\x00" {
			out = append(out, livePromptEntry{directory: entry.directory, prompt: clonePrompt(entry.prompt)})
		}
	}
	return out
}

// runningOnPort lists sessions whose latest event on port reports a turn in
// flight.
func (r *liveStatusRegistry) runningOnPort(port string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for id, entry := range r.entries {
		if entry.port == port && entry.busy {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
