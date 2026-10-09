package autoapprove

import (
	"context"
	"encoding/json"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"strings"
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
	enabled := s.deps.DefaultEnabled
	if s.deps.Store != nil {
		if perSession, exists, err := s.deps.Store.GetAutoApprove(ctx, string(platformID), sessionID); err == nil && exists {
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

func (s *Service) resolveJudgeSections(ctx context.Context, directory, sessionID string) []PromptSection {
	var sections []PromptSection
	if s.deps.Store != nil {
		if stored, err := s.deps.Store.GetPromptSections(ctx); err == nil {
			for _, section := range stored {
				sections = append(sections, PromptSection{Title: section.Title, Content: section.Content, Enabled: section.Enabled})
			}
		}
	}
	if s.judge == nil || s.judge.openCodePort == nil {
		return sections
	}
	port := s.judge.openCodePort(directory)
	if port == "" {
		return sections
	}
	messages := s.judge.recentUserMessages(ctx, port, sessionID)
	if len(messages) == 0 {
		return sections
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
	return append(sections, PromptSection{Title: "Recent user intent", Content: content.String()})
}

func (s *Service) emitChecking(sessionID, permissionID string) func(string) {
	return func(string) {
		payload, err := json.Marshal(map[string]string{"permissionId": permissionID, "sessionID": sessionID})
		if err == nil {
			s.emitSessionSseEvent(sessionID, "ocman.permission.checking", payload)
		}
	}
}
