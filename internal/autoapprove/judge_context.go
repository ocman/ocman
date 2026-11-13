package autoapprove

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxJudgeUserMessageBytes bounds submitted user prose, not tokens or JSON size.
const MaxJudgeUserMessageBytes = 32_000

// JudgeUserContext distinguishes a deliberate context window from failed reads.
// Digest covers all retained user prose, including messages outside the window.
type JudgeUserContext struct {
	Messages []string
	Limited  bool
	Digest   string
}

func judgeHistoryIssue(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "User history refresh timed out. Retry review or approve manually."
	default:
		return "User history could not be loaded. Retry review or approve manually."
	}
}

func typeSafeContext(directory string, sections []PromptSection) (JudgeUserContext, string) {
	if strings.TrimSpace(directory) == "" {
		return JudgeUserContext{}, "Project directory is unavailable. Human review required."
	}
	var collected JudgeUserContext
	for _, section := range sections {
		if section.contextIssue != "" {
			return JudgeUserContext{}, section.contextIssue
		}
		if section.userIntent {
			collected = JudgeUserContext{Messages: section.messages, Limited: section.contextLimited, Digest: section.contextDigest}
		}
	}
	if len(collected.Messages) == 0 {
		return JudgeUserContext{}, "No user messages are available for review. Human review required."
	}
	remaining := MaxJudgeUserMessageBytes
	var recent []string
	for i := len(collected.Messages) - 1; i >= 0; i-- {
		message := collected.Messages[i]
		clipped := len(message) > remaining
		if clipped {
			start := len(message) - remaining
			for start < len(message) && !utf8.RuneStart(message[start]) {
				start++
			}
			message = message[start:]
			collected.Limited = true
		}
		remaining -= len(message)
		if message != "" {
			recent = append(recent, message)
		}
		if remaining == 0 || clipped {
			collected.Limited = collected.Limited || i > 0
			break
		}
	}
	slices.Reverse(recent)
	collected.Messages = recent
	return collected, ""
}
