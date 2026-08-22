package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type livePromptEntry struct {
	directory string
	prompt    platforms.LivePrompt
}

type livePromptRegistry struct {
	mu        sync.RWMutex
	entries   map[string]livePromptEntry
	version   map[string]uint64
	changed   map[string]uint64
	applied   map[string]uint64
	scopePort map[string]string
	portGen   map[string]uint64
}

func newLivePromptRegistry() *livePromptRegistry {
	return &livePromptRegistry{
		entries:   make(map[string]livePromptEntry),
		version:   make(map[string]uint64),
		changed:   make(map[string]uint64),
		applied:   make(map[string]uint64),
		scopePort: make(map[string]string),
		portGen:   make(map[string]uint64),
	}
}

func promptKey(kind, sessionID, requestID string) string {
	return kind + "\x00" + sessionID + "\x00" + requestID
}

func promptScope(directory, kind string) string { return directory + "\x00" + kind }

func promptString(prompt platforms.LivePrompt, key string) string {
	value, _ := prompt[key].(string)
	return value
}

func clonePrompt(prompt platforms.LivePrompt) platforms.LivePrompt {
	out := make(platforms.LivePrompt, len(prompt))
	for key, value := range prompt {
		out[key] = value
	}
	return out
}

// ObservePromptAsked upserts one permission or question observed on the
// process-wide event stream.
func (a *Adapter) ObservePromptAsked(port, directory, kind string, prompt platforms.LivePrompt) {
	a.observePromptAsked(port, 0, directory, kind, prompt)
}

// ObservePromptAskedFromPort ignores events from a stream invalidated by
// process removal while its final buffered event was still dispatching.
func (a *Adapter) ObservePromptAskedFromPort(port string, generation uint64, directory, kind string, prompt platforms.LivePrompt) {
	a.observePromptAsked(port, generation, directory, kind, prompt)
}

func (a *Adapter) observePromptAsked(port string, generation uint64, directory, kind string, prompt platforms.LivePrompt) {
	if a == nil || a.prompts == nil {
		return
	}
	sessionID := promptString(prompt, "sessionID")
	requestID := promptString(prompt, "id")
	if sessionID == "" || requestID == "" {
		return
	}
	a.prompts.mu.Lock()
	if generation != 0 && a.prompts.portGen[port] != generation {
		a.prompts.mu.Unlock()
		return
	}
	key := promptKey(kind, sessionID, requestID)
	a.prompts.entries[key] = livePromptEntry{directory: directory, prompt: clonePrompt(prompt)}
	scope := promptScope(directory, kind)
	a.prompts.version[scope]++
	a.prompts.changed[key] = a.prompts.version[scope]
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
	// Read through the cache: the raw query is a full scan with
	// json_extract over every message, and this runs on every SSE
	// reconnect for every discovered instance. Uncached it pinned a
	// core and starved the 4-connection read pool, which is what made
	// message sends and permission replies time out.
	sessions, err := getSessionsCached(context.Background(), a.db, "", 0)
	if err != nil {
		return out
	}
	for _, session := range sessions {
		if session.Status != db.StatusBusy {
			continue
		}
		root := normalizePortDirectory(foldWorktreeToProjectRoot(session.Directory))
		if !seen[root] || seen[normalizePortDirectory(session.Directory)] {
			continue
		}
		seen[normalizePortDirectory(session.Directory)] = true
		out = append(out, session.Directory)
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
	defer r.mu.Unlock()
	scope := promptScope(directory, kind)
	if token < r.applied[scope] {
		return false
	}
	r.applied[scope] = token
	for key, entry := range r.entries {
		if entry.directory == directory && strings.HasPrefix(key, kind+"\x00") && r.changed[key] <= token {
			delete(r.entries, key)
		}
	}
	for _, prompt := range prompts {
		sessionID := promptString(prompt, "sessionID")
		requestID := promptString(prompt, "id")
		key := promptKey(kind, sessionID, requestID)
		if sessionID != "" && requestID != "" && r.changed[key] <= token {
			cloned := clonePrompt(prompt)
			r.entries[key] = livePromptEntry{directory: directory, prompt: cloned}
			if onPrompt != nil {
				onPrompt(clonePrompt(cloned))
			}
		}
	}
	return true
}

func (r *livePromptRegistry) find(kind, requestID string) (livePromptEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for key, entry := range r.entries {
		if strings.HasPrefix(key, kind+"\x00") && promptString(entry.prompt, "id") == requestID {
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
		sessionID := promptString(entry.prompt, "sessionID")
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
