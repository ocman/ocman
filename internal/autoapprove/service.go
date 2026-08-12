// Package autoapprove implements ocman's LLM-judged permission
// auto-approval pipeline: an SSE tee that observes permission.asked
// events, a per-permission state machine with dedup and cancellation,
// a transient-session LLM judge, a per-session safe-command cache, and
// a headless watcher that subscribes to every running OpenCode
// instance's /global/event stream.
//
// The HTTP layer lives in internal/server; it talks to this package
// through Service, whose external dependencies are injected via Deps
// (using the same lazy service pattern as other server services).
package autoapprove

import (
	"errors"
	"io"
	"runtime/debug"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// errNoSessionDirResolver is returned by ResolveSessionDir when no
// SessionDir dependency is wired, so the judge falls through to human
// review rather than proceeding without a directory.
var errNoSessionDirResolver = errors.New("no session directory resolver")

// SettingsStore is the slice of *state.DB the pipeline needs. Nil is
// allowed everywhere it is consulted (settings fall back to defaults
// and approvals simply aren't persisted).
type SettingsStore interface {
	GetAutoApprove(platform, sessionID string) (enabled bool, exists bool, err error)
	GetJudgeDelayMs() (int64, error)
	GetPromptSections() ([]state.PromptSection, error)
	GetSetting(key string) (value string, ok bool, err error)
	RecordApprovedPermission(platform, sessionID string, p state.ApprovedPermission) error
}

// Deps bundles the service's external dependencies. Every func field
// is optional — nil fields turn the corresponding side effect into a
// no-op, which is what the tests rely on.
type Deps struct {
	// OpenCodeAuth authenticates judge and headless watcher traffic.
	OpenCodeAuth ocapi.Auth

	// Store persists/loads settings and approval audit rows. May be nil.
	Store SettingsStore

	// SessionDir resolves a session ID to its working directory (used
	// for OpenCode port discovery). May be nil when no session DB is
	// available; the judge then falls through to human review.
	SessionDir func(sessionID string) (string, error)

	// ParentSessionID resolves a (possibly child) session ID to its
	// parent session ID, returning ok=false when the session is not a
	// tracked child (or no resolver is wired). Used so a child session
	// inherits the parent's safe-command cache: a command the parent
	// already had auto-approved is approved for the child without a
	// fresh judge run or a user prompt. May be nil.
	ParentSessionID func(childID string) (parentID string, ok bool)

	// OpencodePlatform resolves the OpenCode platform adapter for the
	// headless watcher. May be nil / return nil; Ensure fails safe.
	OpencodePlatform func() platforms.Platform

	// Broadcast hooks fan events out to every connected client. All
	// optional.
	BroadcastPermissionResolved func(sessionID, permissionID, reason string)
	BroadcastQuestionResolved   func(sessionID, requestID, reason string)
	// BroadcastSessionIdle carries the platform the edge came from: a
	// session's identity is (platform, sessionID), and the consumer drains
	// that session's message queue with it.
	BroadcastSessionIdle func(platformID, sessionID string)
	BroadcastSessionChanged     func(sessionID string)
	BroadcastSessionStatus      func(sessionID string, status db.SessionStatus)
	BroadcastGlobalEvent        func(event string, data []byte)

	// DefaultEnabled is the server-wide auto-approve default applied
	// when a session has no per-session override.
	DefaultEnabled bool
}

// Service owns the auto-approve pipeline state for the lifetime of the
// ocman process. The zero value is usable in tests; production code
// uses NewService so the judge is wired against real port discovery.
type Service struct {
	deps Deps

	// judge runs the LLM verdict. Nil in some tests.
	judge *PermissionJudge

	// judgeDelayMs is the cached value of the judge delay setting.
	// Seeded at startup and updated whenever the setting is changed via
	// the API — written from an HTTP handler and read from the watcher
	// and SSE tee goroutines, hence atomic.
	judgeDelayMs atomic.Int64

	// sseSessions maps sessionID -> the live SSE writer for any
	// currently-connected client. See RegisterSink.
	sseSessions   map[string]*Sink
	sseSessionsMu sync.Mutex

	// autoApprove tracks the per-permission state of the pipeline.
	// Keyed by "sessionID|permissionID". See autoApproveStatus.
	autoApprove   map[string]*autoApproveStatus
	autoApproveMu sync.Mutex

	// safeCommandCache remembers safe Bash-command verdicts per
	// session keyed by md5(metadata["command"]). See commandHash.
	safeCommandCache   map[string]map[string]string
	safeCommandCacheMu sync.Mutex

	// askedCache remembers the permission text + patterns from a
	// permission.asked event, keyed by "sessionID|permissionID", so a
	// later permission.replied("always") can be persisted with the
	// original patterns (the replied event carries neither). Bounded
	// (see askedCacheMax) and evicted on reply. See HandlePermissionReplied.
	askedCache   map[string]askedPermission
	askedCacheMu sync.Mutex
}

// NewService returns a Service wired against the real OpenCode port
// discovery.
func NewService(deps Deps) *Service {
	return &Service{
		deps:             deps,
		judge:            newPermissionJudge(deps.OpenCodeAuth),
		sseSessions:      make(map[string]*Sink),
		autoApprove:      make(map[string]*autoApproveStatus),
		safeCommandCache: make(map[string]map[string]string),
		askedCache:       make(map[string]askedPermission),
	}
}

// ResolveSessionDir resolves a session ID to its working directory via
// the injected SessionDir dependency. Returns an error when no
// resolver is wired (so the judge falls through to human review). Kept
// exported so the server-side adapter wiring can be tested end-to-end.
func (s *Service) ResolveSessionDir(sessionID string) (string, error) {
	if s == nil || s.deps.SessionDir == nil {
		return "", errNoSessionDirResolver
	}
	return s.deps.SessionDir(sessionID)
}

// ResolveParentSessionID resolves a (possibly child) session ID to its
// parent via the injected ParentSessionID dependency, returning
// ok=false when the session is not a tracked child or no resolver is
// wired. Kept exported so the server-side closure wiring can be tested
// end-to-end (mirrors ResolveSessionDir).
func (s *Service) ResolveParentSessionID(childID string) (string, bool) {
	if s == nil || s.deps.ParentSessionID == nil {
		return "", false
	}
	return s.deps.ParentSessionID(childID)
}

// OpencodeAdapter returns the OpenCode platform adapter via the
// injected OpencodePlatform dependency, or nil when none is wired.
func (s *Service) OpencodeAdapter() platforms.Platform {
	if s == nil || s.deps.OpencodePlatform == nil {
		return nil
	}
	return s.deps.OpencodePlatform()
}

// JudgeDelayMs returns the cached judge delay.
func (s *Service) JudgeDelayMs() int64 { return s.judgeDelayMs.Load() }

// SetJudgeDelayMs updates the cached judge delay (called at startup
// seeding and whenever the setting changes via the API).
func (s *Service) SetJudgeDelayMs(ms int64) { s.judgeDelayMs.Store(ms) }

// JudgeModel returns the judge's current model selection. Empty when
// the service has no judge. Used by the settings handler tests to
// assert ReloadJudgeModel applied a change.
func (s *Service) JudgeModel() (provider, modelID string) {
	if s == nil || s.judge == nil {
		return "", ""
	}
	return s.judge.model()
}

// ReloadJudgeModel re-reads the persisted judge model setting and
// applies it to the judge, falling back to the built-in default when
// the setting is unset or malformed. No-op when the service has no
// judge.
func (s *Service) ReloadJudgeModel() {
	if s == nil || s.judge == nil {
		return
	}
	if provider, modelID, ok := loadJudgeModel(s.deps.Store); ok {
		s.judge.setModel(provider, modelID)
	} else {
		s.judge.setModel(judgeModelProvider, judgeModelID)
	}
}

// broadcastPermissionResolved invokes the injected hook, if any.
func (s *Service) broadcastPermissionResolved(sessionID, permissionID, reason string) {
	if s.deps.BroadcastPermissionResolved != nil {
		s.deps.BroadcastPermissionResolved(sessionID, permissionID, reason)
	}
}

// broadcastGlobalEvent invokes the injected hook, if any.
func (s *Service) broadcastGlobalEvent(event string, data []byte) {
	if s.deps.BroadcastGlobalEvent != nil {
		s.deps.BroadcastGlobalEvent(event, data)
	}
}

// runWithRecover runs body while protecting the caller from a panic.
// Duplicated from internal/server's recover.go so the watcher loops
// keep the same never-die guarantee without a dependency on the server
// package.
func runWithRecover(name string, body func()) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{
				"loop":  name,
				"panic": r,
				"stack": string(debug.Stack()),
			}).Error("background loop panicked, continuing on next tick")
		}
	}()
	body()
}

// Sink wraps an SSE response writer with the synchronisation needed
// to safely emit events from background goroutines.
//
// The auto-approve pipeline emits events long after the triggering
// permission.asked has been processed (after the configured delay +
// judge execution). The originating SSE connection may have closed in
// the meantime; without coordination, writes to the underlying
// http.ResponseWriter would panic the moment Go's connection
// bookkeeping recycles the bufio.Writer.
//
// close() sets closed=true under mu so any concurrent write either
// completes against the live writer or sees closed=true and skips. All
// writes go through write(), which takes the same mutex.
type Sink struct {
	w      io.Writer
	flush  func()
	mu     sync.Mutex
	closed bool
}

// write emits a single named SSE event. It is a no-op if the sink has
// been closed (the originating connection went away). Safe to call
// concurrently with close() and with other write() calls — they
// serialise on the sink's mutex.
func (s *Sink) write(eventType string, data []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	WriteSSEEvent(s.w, s.flush, eventType, data)
}

// close marks the sink as closed so future write() calls become no-ops.
// Idempotent.
func (s *Sink) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
