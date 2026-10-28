package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ocman/ocman/internal/platforms/claude/launch"
)

// SessionTitle runs a tool-free Haiku helper without saving a transcript or
// taking one of the interactive session slots. The original turn is untouched.
func SessionTitle(ctx context.Context, slots, cli, dir, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, err := HelperSlot(ctx, slots)
	if err != nil {
		return "", err
	}
	defer release()
	runes := []rune(prompt)
	if len(runes) > 2000 {
		runes = append(runes[:1500], runes[len(runes)-500:]...)
	}
	args := append(append([]string{}, worktreeNameArgs...), "--system-prompt", sessionTitlePrompt)
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Dir = dir
	cmd.Env = claudecli.Environment(nil, dir)
	// JSON quoting prevents prompt content from closing the data wrapper.
	content, _ := json.Marshal(string(runes))
	cmd.Stdin = strings.NewReader("<session>\n" + string(content) + "\n</session>\nWrite the title in the predominant language of the session; keep technical terms and code identifiers in their original form.")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var result struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", err
	}
	title := strings.TrimSpace(result.Title)
	if title == "" || utf8.RuneCountInString(title) > 200 || strings.ContainsFunc(title, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", errors.New("claude returned an invalid session title")
	}
	return title, nil
}
