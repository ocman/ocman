package autoapprove

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Judge evaluates a permission without owning the approval or cache policy.
// A backend may report an in-flight judge session through onSessionCreated.
type Judge interface {
	JudgeWithCallback(ctx context.Context, directory, permission string, patterns []string, metadata map[string]any, customSections []PromptSection, onSessionCreated func(string)) JudgeResult
}

const verdictNeedsUser judgeVerdict = "needs-user"

// judgePermission selects a backend for each permission, after the cache gates.
func (s *Service) judgePermission(ctx context.Context, platformID platforms.ID, directory, permission string, patterns []string, metadata map[string]any, sections []PromptSection, onSessionCreated func(string)) JudgeResult {
	var backend Judge
	config, err := LoadJudgeEndpoint(ctx, s.deps.Store)
	if err != nil {
		return endpointFailure("Could not read reviewer endpoint settings.")
	}
	if config.Format != "" {
		backend = s.judge
	} else if s.deps.SelectJudge != nil {
		backend = s.deps.SelectJudge(platformID, directory)
	} else if s.judge != nil && s.judge.openCodePort != nil && s.judge.openCodePort(directory) != "" {
		backend = s.judge
	} else if s.deps.FallbackJudge != nil {
		backend = s.deps.FallbackJudge
	}
	if backend == nil {
		return JudgeResult{Verdict: verdictNeedsUser, Reasoning: "No auto-approve judge backend is available; please review this permission."}
	}
	return backend.JudgeWithCallback(ctx, directory, permission, patterns, metadata, sections, onSessionCreated)
}
