package autoapprove

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

type judgeVerdict string

const (
	verdictSafe          judgeVerdict = "safe"
	verdictUnsafe        judgeVerdict = "unsafe"
	judgeTimeout                      = 30 * time.Second
	judgeModelProvider                = "anthropic"
	judgeModelID                      = "claude-haiku-4-5"
	JudgeModelSettingKey              = "judge_model"
	DefaultJudgeModel                 = judgeModelProvider + "/" + judgeModelID
	judgeAgent                        = "build"
)

type judgeModelStore interface {
	GetSetting(context.Context, string) (string, bool, error)
}

func loadJudgeModel(ctx context.Context, db judgeModelStore) (provider, modelID string, ok bool) {
	if db == nil {
		return "", "", false
	}
	val, found, err := db.GetSetting(ctx, JudgeModelSettingKey)
	if err != nil || !found {
		return "", "", false
	}
	i := strings.IndexByte(val, '/')
	if i <= 0 || i == len(val)-1 {
		return "", "", false
	}
	return val[:i], val[i+1:], true
}

// PermissionJudge uses OpenCode by default, or a configured external endpoint.
// Every transport or parsing failure falls through to human review.
type PermissionJudge struct {
	sessionIdentity func(context.Context, string) (string, error)
	openCodePort    func(directory string) string
	httpClient      *http.Client
	store           judgeModelStore
	recordUsage     func(context.Context, string, *int64, *int64) error
	modelMu         sync.RWMutex
	modelProvider   string
	modelID         string
	// helpers holds the native and public IDs of live judge helper sessions,
	// so prompts they raise never enter the auto-approve pipeline.
	helpers sync.Map
	// slots bounds concurrent OpenCode judgments (session + LLM turn), like
	// the Claude judge's helper slots. Nil means unbounded.
	slots chan struct{}
}

// maxConcurrentOpenCodeJudges matches the Claude helper-slot cap.
const maxConcurrentOpenCodeJudges = 2

// acquireSlot waits for a judgment slot without holding any lock. It fails
// when ctx ends first, so a waiter never outlives the judge timeout.
func (j *PermissionJudge) acquireSlot(ctx context.Context) (func(), error) {
	if j.slots == nil {
		return func() {}, nil
	}
	select {
	case j.slots <- struct{}{}:
		return func() { <-j.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// trackHelper marks id as a live judge helper session; the returned func
// clears the mark.
func (j *PermissionJudge) trackHelper(id string) func() {
	j.helpers.Store(id, struct{}{})
	return func() { j.helpers.Delete(id) }
}

// helperSession reports whether id is a live judge helper session.
func (j *PermissionJudge) helperSession(id string) bool {
	if j == nil || id == "" {
		return false
	}
	_, ok := j.helpers.Load(id)
	return ok
}

func (j *PermissionJudge) model() (provider, modelID string) {
	if j == nil {
		return "", ""
	}
	j.modelMu.RLock()
	defer j.modelMu.RUnlock()
	return j.modelProvider, j.modelID
}

func (j *PermissionJudge) setModel(provider, modelID string) {
	if j == nil {
		return
	}
	j.modelMu.Lock()
	defer j.modelMu.Unlock()
	j.modelProvider, j.modelID = provider, modelID
}

func newPermissionJudge(auth ocapi.Auth) *PermissionJudge {
	return &PermissionJudge{
		httpClient:    &http.Client{Timeout: judgeTimeout, Transport: auth.Transport(http.DefaultTransport)},
		modelProvider: judgeModelProvider,
		modelID:       judgeModelID,
		slots:         make(chan struct{}, maxConcurrentOpenCodeJudges),
	}
}

type JudgeResult struct {
	Verdict          judgeVerdict
	EvaluationFailed bool
	// Only used by the in-flight callback. Completed judge sessions are deleted.
	SessionID  string
	Reasoning  string
	cacheScope string      // Configuration actually used, even if settings change mid-call.
	ruleScores []ruleScore // Independent Jev rule probabilities; never a verdict distribution.
	usage      *typeSafeUsage
}

func (j *PermissionJudge) JudgeWithCallback(ctx context.Context, directory, permission string, patterns []string, metadata map[string]any, customSections []PromptSection, onSessionCreated func(judgeSessionID string)) (result JudgeResult) {
	if j == nil {
		return JudgeResult{Verdict: verdictNeedsUser}
	}
	ctx, cancel := context.WithTimeout(ctx, judgeTimeout)
	defer cancel()
	config, err := LoadJudgeEndpoint(ctx, j.store)
	if err != nil {
		return endpointFailure("Could not read reviewer endpoint settings.")
	}
	defer func() { result.cacheScope = endpointCacheScope(config) }()
	if config.Format != "" {
		// External checks have no transient session, but still publish checking.
		if onSessionCreated != nil {
			onSessionCreated("")
		}
		result = judgeEndpoint(ctx, config, directory, permission, patterns, metadata, customSections)
		j.recordTypeSafeUsage(ctx, config, result.usage)
		return result
	}
	if j.openCodePort == nil {
		return JudgeResult{Verdict: verdictNeedsUser}
	}
	port := j.openCodePort(directory)
	if port == "" {
		log.WithField("directory", directory).Warn("auto-approve judge: no running OpenCode instance found, falling through to human")
		return JudgeResult{Verdict: verdictNeedsUser}
	}
	release, err := j.acquireSlot(ctx)
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: no judge slot before the timeout, falling through to human")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	defer release()
	sessionID, err := j.createSession(ctx, port, directory, "(auto-approve subagent)")
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to create judge session")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	defer j.trackHelper(sessionID)()
	// A cancelled judgment still gets a fresh context for best-effort cleanup.
	defer func() {
		delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer delCancel()
		if err := j.deleteSession(delCtx, port, sessionID); err != nil {
			log.WithError(err).WithField("judgeSessionID", sessionID).Warn("auto-approve judge: failed to delete judge session")
		}
	}()
	publicID := sessionID
	if j.sessionIdentity != nil {
		publicID, err = j.sessionIdentity(ctx, sessionID)
		if err != nil {
			return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
		}
	}
	if publicID != sessionID {
		defer j.trackHelper(publicID)()
	}
	if onSessionCreated != nil {
		onSessionCreated(publicID)
	}
	if err := j.sendPrompt(ctx, port, sessionID, permission, patterns, metadata, customSections); err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to send prompt")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	text, err := j.collectResponse(ctx, port, sessionID)
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to collect response")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	verdict, reasoning, valid := parseJudgeResponse(text)
	return JudgeResult{Verdict: verdict, Reasoning: reasoning, EvaluationFailed: !valid}
}
