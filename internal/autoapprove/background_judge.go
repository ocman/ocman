package autoapprove

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
)

type judgeInputs struct {
	directory string
	delay     int64
	sections  []PromptSection
}

func (s *Service) prepareAutoApprove(ctx context.Context, platformID platforms.ID, sessionID, permissionID string, asked askedPermission) (askedPermission, bool) {
	if asked.lifecycleObserved {
		return asked, asked.lifecycleEnabled
	}
	enabled := s.defaultEnabled()
	if s.deps.Store != nil {
		perSession, exists, err := s.deps.Store.GetAutoApprove(ctx, string(platformID), sessionID)
		switch {
		case err != nil:
			// Fail closed: the session may have been explicitly disabled.
			log.WithError(err).WithField("sessionID", sessionID).
				Warn("auto-approve: reading session setting failed, leaving the prompt to the user")
			enabled = false
		case exists:
			enabled = perSession
		}
	}
	var directory string
	var directoryErr error
	if enabled {
		directory, directoryErr = s.ResolveSessionDir(sessionID)
	}
	return s.observeAskedLifecycle(sessionID, permissionID, asked, enabled, directory, directoryErr), enabled
}

func (s *Service) resolveJudgeInputs(ctx context.Context, directory string, directoryErr error, sessionID string) (judgeInputs, error) {
	if directoryErr != nil {
		return judgeInputs{}, directoryErr
	}
	inputs := judgeInputs{directory: directory, delay: s.judgeDelayMs.Load()}
	if s.deps.Store != nil {
		if delay, err := s.deps.Store.GetJudgeDelayMs(ctx); err == nil {
			inputs.delay = delay
		}
		if provider, modelID, ok := loadJudgeModel(ctx, s.deps.Store); ok && s.judge != nil {
			s.judge.setModel(provider, modelID)
		}
	}
	return inputs, nil
}

func (s *Service) resolveJudgeSections(ctx context.Context, adapter platforms.Platform, directory, sessionID string) []PromptSection {
	var sections []PromptSection
	if s.deps.Store != nil {
		if stored, err := s.deps.Store.GetPromptSections(ctx); err == nil {
			for _, section := range stored {
				sections = append(sections, PromptSection{Title: section.Title, Content: section.Content, Enabled: section.Enabled})
			}
		} else {
			sections = append(sections, PromptSection{contextUnavailable: true})
		}
	}
	var messages []string
	if s.deps.RecentUserMessages != nil && adapter != nil {
		var err error
		messages, err = s.deps.RecentUserMessages(ctx, adapter.ID(), sessionID)
		if err != nil {
			messages = nil
		}
	}
	judgeMessages := messages
	if config, err := LoadJudgeEndpoint(ctx, s.deps.Store); err == nil && config.Format == "typesafe" && s.deps.JudgeUserMessages != nil && adapter != nil {
		judgeMessages, err = s.deps.JudgeUserMessages(ctx, adapter.ID(), sessionID)
		if err != nil {
			judgeMessages = nil
		}
	}
	if len(messages) == 0 {
		if len(judgeMessages) == 0 {
			return sections
		}
		return append(sections, PromptSection{userIntent: true, messages: judgeMessages})
	}
	var content strings.Builder
	content.WriteString("The user recently sent these messages (oldest first):\n")
	for _, message := range messages {
		if len(message) > 300 {
			message = message[:300] + "…"
		}
		content.WriteString("  - " + message + "\n")
	}
	content.WriteString("\nIf the permission request is a direct and proportionate consequence of what the user asked for, lean toward SAFE.")
	return append(sections, PromptSection{Title: "Recent user intent", Content: content.String(), userIntent: true, messages: judgeMessages})
}

func (s *Service) emitChecking(sessionID, permissionID string) func(string) {
	return func(string) {
		payload, err := json.Marshal(map[string]string{"permissionId": permissionID, "sessionID": sessionID})
		if err == nil {
			s.emitSessionSseEvent(sessionID, "ocman.permission.checking", payload)
		}
	}
}

// A changed policy or user restriction invalidates an in-flight Jev approval.
func (s *Service) ruleReviewCurrent(ctx context.Context, adapter platforms.Platform, directory, sessionID string, sections []PromptSection, result JudgeResult) bool {
	if len(result.ruleScores) == 0 {
		return true
	}
	config, err := LoadJudgeEndpoint(ctx, s.deps.Store)
	if err != nil || config.Format != "typesafe" || endpointCacheScope(config) != result.cacheScope {
		return false
	}
	return reflect.DeepEqual(sections, s.resolveJudgeSections(ctx, adapter, directory, sessionID))
}
