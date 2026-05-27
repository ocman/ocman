// Package mcp implements the ocman MCP (Model Context Protocol) server.
// It exposes tools for splitting work from an active coding session into
// new parallel sessions or isolated git worktrees.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
)

const (
	// defaultMaxPromptChars is the default character cap for composed prompts.
	// Truncation removes recent_messages first, then git_diff_stat, preserving
	// intent and project_metadata always.
	defaultMaxPromptChars = 8000

	// defaultRecentMessageCount is the default number of recent messages to
	// include from the parent session's conversation history.
	defaultRecentMessageCount = 10
)

// ContextOptions controls which context sources are injected into the
// composed prompt. All sources are enabled by default.
type ContextOptions struct {
	RecentMessages bool
	RelevantFiles  bool
	GitBranch      bool
	GitDiffStat    bool
	ProjectMeta    bool
	MaxChars       int
}

// DefaultContextOptions returns a ContextOptions with all sources enabled
// and the default character cap.
func DefaultContextOptions() ContextOptions {
	return ContextOptions{
		RecentMessages: true,
		RelevantFiles:  true,
		GitBranch:      true,
		GitDiffStat:    true,
		ProjectMeta:    true,
		MaxChars:       defaultMaxPromptChars,
	}
}

// sessionReader is the subset of db.DB used by PromptComposer. Defined
// as an interface so tests can inject a fake without a real SQLite DB.
type sessionReader interface {
	GetSession(sessionID string) (*db.Session, error)
	GetSessionMessages(sessionID string) ([]db.Message, error)
}

// gitRunner abstracts the git CLI calls used by PromptComposer so tests
// can inject a fake without requiring a real git repository.
type gitRunner func(ctx context.Context, dir string, args ...string) (string, error)

// defaultGitRunner runs git in the given directory and returns stdout.
func defaultGitRunner(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// PromptComposer assembles an enriched prompt for a child session from
// the caller's intent and automatically extracted context sources.
type PromptComposer struct {
	db         sessionReader
	runGit     gitRunner
}

// NewPromptComposer creates a PromptComposer backed by the given DB.
func NewPromptComposer(database sessionReader) *PromptComposer {
	return &PromptComposer{
		db:     database,
		runGit: defaultGitRunner,
	}
}

// withGitRunner returns a copy of the composer with a custom git runner.
// Used in tests to inject a fake without a real git repository.
func (c *PromptComposer) withGitRunner(r gitRunner) *PromptComposer {
	return &PromptComposer{db: c.db, runGit: r}
}

// Compose assembles the enriched prompt. It collects each enabled context
// source, builds a Markdown document, and truncates to opts.MaxChars if
// needed. If opts.MaxChars is 0, defaultMaxPromptChars is used.
//
// Errors from individual context sources are silently ignored (the source
// is omitted from the prompt) so a missing git repo or empty session does
// not prevent the split from proceeding.
func (c *PromptComposer) Compose(ctx context.Context, sessionID, intent string, opts ContextOptions) (string, error) {
	if opts.MaxChars <= 0 {
		opts.MaxChars = defaultMaxPromptChars
	}

	// Fetch session metadata (cwd, title) — always needed.
	session, err := c.db.GetSession(sessionID)
	if err != nil {
		// If we can't find the session at all, we can still compose a
		// minimal prompt from the intent alone.
		session = &db.Session{ID: sessionID}
	}

	// --- Collect context sources ---

	var projectMeta string
	if opts.ProjectMeta {
		projectMeta = buildProjectMeta(session)
	}

	var gitBranch string
	if opts.GitBranch && session.Directory != "" {
		if branch, err := c.runGit(ctx, session.Directory, "branch", "--show-current"); err == nil {
			gitBranch = branch
		}
	}

	var gitDiffStat string
	if opts.GitDiffStat && session.Directory != "" {
		if diff, err := c.runGit(ctx, session.Directory, "diff", "--stat"); err == nil {
			gitDiffStat = diff
		}
	}

	var recentMessages []db.Message
	var relevantFiles []string
	if opts.RecentMessages || opts.RelevantFiles {
		msgs, err := c.db.GetSessionMessages(sessionID)
		if err == nil {
			// Take the last N messages.
			if len(msgs) > defaultRecentMessageCount {
				msgs = msgs[len(msgs)-defaultRecentMessageCount:]
			}
			recentMessages = msgs
			if opts.RelevantFiles {
				relevantFiles = extractFilePaths(msgs)
			}
		}
	}

	// --- Assemble the prompt ---

	var buf bytes.Buffer

	// Intent is always first and always preserved.
	fmt.Fprintf(&buf, "## Task\n\n%s\n\n", strings.TrimSpace(intent))

	// Project metadata (always preserved, small).
	if projectMeta != "" {
		fmt.Fprintf(&buf, "## Project\n\n%s\n\n", projectMeta)
	}

	// Git branch (small, always preserved).
	if gitBranch != "" {
		fmt.Fprintf(&buf, "## Current Branch\n\n`%s`\n\n", gitBranch)
	}

	// Relevant files (small, always preserved).
	if opts.RelevantFiles && len(relevantFiles) > 0 {
		fmt.Fprintf(&buf, "## Relevant Files\n\n")
		for _, f := range relevantFiles {
			fmt.Fprintf(&buf, "- `%s`\n", f)
		}
		fmt.Fprintf(&buf, "\n")
	}

	// Git diff stat — truncatable.
	diffStatSection := ""
	if gitDiffStat != "" {
		diffStatSection = fmt.Sprintf("## Uncommitted Changes\n\n```\n%s\n```\n\n", gitDiffStat)
	}

	// Recent messages — truncatable (most likely to be large).
	recentSection := ""
	if opts.RecentMessages && len(recentMessages) > 0 {
		recentSection = buildRecentMessagesSection(recentMessages)
	}

	// Apply character cap: add sections in order of importance, dropping
	// the largest ones first if we're over budget.
	base := buf.String()
	remaining := opts.MaxChars - len(base)

	if remaining > 0 && diffStatSection != "" {
		if len(diffStatSection) <= remaining {
			base += diffStatSection
			remaining -= len(diffStatSection)
		}
		// else: diff stat is too large, skip it
	}

	if remaining > 0 && recentSection != "" {
		if len(recentSection) <= remaining {
			base += recentSection
		} else {
			// Truncate the recent messages section to fit.
			truncated := recentSection[:remaining-3] + "..."
			base += truncated
		}
	}

	return strings.TrimRight(base, "\n") + "\n", nil
}

// buildProjectMeta formats the project metadata section.
func buildProjectMeta(s *db.Session) string {
	var parts []string
	if s.Directory != "" {
		parts = append(parts, fmt.Sprintf("- **Directory**: `%s`", s.Directory))
	}
	if s.Title != "" {
		parts = append(parts, fmt.Sprintf("- **Session title**: %s", s.Title))
	}
	return strings.Join(parts, "\n")
}

// buildRecentMessagesSection formats the recent messages as a Markdown section.
func buildRecentMessagesSection(msgs []db.Message) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "## Recent Conversation\n\n")
	for _, m := range msgs {
		role := extractRole(m.Data)
		text := extractText(m.Data)
		if text == "" {
			continue
		}
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		fmt.Fprintf(&buf, "**%s**: %s\n\n", role, text)
	}
	return buf.String()
}

// extractRole extracts the role field from a message's JSON data.
func extractRole(data json.RawMessage) string {
	var msg struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &msg); err != nil || msg.Role == "" {
		return "unknown"
	}
	return msg.Role
}

// extractText extracts the text content from a message's JSON data.
// It handles both the flat "text" field and the "parts" array format.
func extractText(data json.RawMessage) string {
	var msg struct {
		Text  string `json:"text"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return ""
	}
	if msg.Text != "" {
		return msg.Text
	}
	for _, p := range msg.Parts {
		if p.Type == "text" && p.Text != "" {
			return p.Text
		}
	}
	return ""
}

// filePathPattern matches likely file paths in message text.
// Matches strings starting with / or ./ that contain at least one
// path separator and end with a word character or common extension.
var filePathPattern = regexp.MustCompile(`(?:^|[\s"'` + "`" + `(])(\./[\w./\-]+|/[\w./\-]+)`)

// extractFilePaths scans message text for file path mentions and returns
// a deduplicated, ordered list of unique paths.
func extractFilePaths(msgs []db.Message) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, m := range msgs {
		text := extractText(m.Data)
		if text == "" {
			continue
		}
		for _, match := range filePathPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			p := strings.TrimSpace(match[1])
			if p != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return paths
}
