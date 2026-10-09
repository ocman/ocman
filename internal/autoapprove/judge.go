package autoapprove

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	opencode "github.com/NoUseFreak/ocman/internal/platforms/opencode"
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
	openCodePort  func(directory string) string
	httpClient    *http.Client
	store         judgeModelStore
	modelMu       sync.RWMutex
	modelProvider string
	modelID       string
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
		openCodePort:  opencode.DiscoverOpenCodePort,
		httpClient:    &http.Client{Timeout: judgeTimeout, Transport: auth.Transport(http.DefaultTransport)},
		modelProvider: judgeModelProvider,
		modelID:       judgeModelID,
	}
}

type JudgeResult struct {
	Verdict          judgeVerdict
	EvaluationFailed bool
	// Only used by the in-flight callback. Completed judge sessions are deleted.
	SessionID  string
	Reasoning  string
	cacheScope string // Configuration actually used, even if settings change mid-call.
}

func (j *PermissionJudge) JudgeWithCallback(ctx context.Context, directory, permission string, patterns []string, metadata map[string]any, customSections []PromptSection, onSessionCreated func(judgeSessionID string)) (result JudgeResult) {
	if j == nil {
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
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
		return judgeEndpoint(ctx, config, permission, patterns, metadata, customSections)
	}
	if j.openCodePort == nil {
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	port := j.openCodePort(directory)
	if port == "" {
		log.WithField("directory", directory).Warn("auto-approve judge: no running OpenCode instance found, falling through to human")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	sessionID, err := j.createSession(ctx, port, directory, "(auto-approve subagent)")
	if err != nil {
		log.WithError(err).Warn("auto-approve judge: failed to create judge session")
		return JudgeResult{Verdict: verdictUnsafe, EvaluationFailed: true}
	}
	// A cancelled judgment still gets a fresh context for best-effort cleanup.
	defer func() {
		delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer delCancel()
		if err := j.deleteSession(delCtx, port, sessionID); err != nil {
			log.WithError(err).WithField("judgeSessionID", sessionID).Warn("auto-approve judge: failed to delete judge session")
		}
	}()
	if onSessionCreated != nil {
		onSessionCreated(sessionID)
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
