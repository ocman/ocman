package codexprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// Built-in slash commands; every other command names an enabled skill.
var builtinCommands = []platforms.SlashCommandEntry{
	{Name: "review", Description: "Review uncommitted changes, or follow the given review instructions", Source: "command"},
	{Name: "compact", Description: "Summarize the conversation to free context", Source: "command"},
}

// Commands lists the built-in commands and the enabled skills Codex reports
// for a directory, each skill once.
func Commands(entries []codexappserver.SkillsEntry) []platforms.SlashCommandEntry {
	out := append([]platforms.SlashCommandEntry{}, builtinCommands...)
	seen := map[string]bool{"review": true, "compact": true}
	for _, e := range entries {
		for _, s := range e.Skills {
			if !s.Enabled || seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			desc := s.ShortDescription
			if desc == "" {
				desc = s.Description
			}
			out = append(out, platforms.SlashCommandEntry{Name: s.Name, Description: desc, Source: "skill"})
		}
	}
	return out
}

// compact summarizes the thread; Codex runs it as a turn.
func (b *backend) compact(ctx context.Context, id string) error {
	return b.runTurn(ctx, id, "", func(c *codexappserver.Client) (string, error) { return "", c.CompactThread(ctx, id) })
}

// shell runs a `!` command as its own turn. Codex runs it unsandboxed with
// full access, like a terminal; it does not reach the model.
func (b *backend) shell(ctx context.Context, id, command string) error {
	if strings.TrimSpace(command) == "" {
		return statusError{http.StatusBadRequest, errors.New("command is empty")}
	}
	return b.runTurn(ctx, id, "", func(c *codexappserver.Client) (string, error) { return "", c.ShellCommand(ctx, id, command) })
}

// command runs a slash command: /review [instructions], /compact, or an
// enabled skill with the arguments as its prompt.
func (b *backend) command(ctx context.Context, id string, req platforms.ExecuteCommandRequest) error {
	name, args := strings.TrimPrefix(strings.TrimSpace(req.Command), "/"), strings.TrimSpace(req.Arguments)
	switch name {
	case "compact":
		return b.compact(ctx, id)
	case "review":
		target := codexappserver.ReviewTarget{Type: "uncommittedChanges"}
		if args != "" {
			target = codexappserver.ReviewTarget{Type: "custom", Instructions: args}
		}
		return b.runTurn(ctx, id, "", func(c *codexappserver.Client) (string, error) {
			turn, err := c.StartReview(ctx, codexappserver.ReviewStartParams{ThreadID: id, Target: target})
			if err != nil {
				return "", err
			}
			return turn.ID, nil
		})
	}
	skill, err := b.skill(ctx, id, name)
	if err != nil {
		return err
	}
	input := []codexappserver.UserInput{{Type: "skill", Name: skill.Name, Path: skill.Path}}
	if args != "" {
		input = append(input, codexappserver.TextInput(args))
	}
	return b.startTurn(ctx, id, input, req.Model, req.Reasoning)
}

// skill finds an enabled skill of the session's directory.
func (b *backend) skill(ctx context.Context, id, name string) (*codexappserver.Skill, error) {
	c, gen, err := b.conn(ctx)
	if err != nil {
		return nil, err
	}
	if err := b.load(ctx, c, gen, id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	dir := b.sessionLocked(id).dir
	b.mu.Unlock()
	entries, err := c.ListSkills(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		for _, s := range e.Skills {
			if s.Name == name && s.Enabled {
				return &s, nil
			}
		}
	}
	return nil, statusError{http.StatusNotFound, fmt.Errorf("unknown command /%s", name)}
}

// fork branches a thread. With a message, the fork keeps the turns before
// the one holding it, as a fork from a chosen prompt does elsewhere; a
// message in the first turn forks to an empty thread in the same directory.
// Errors before Codex allocates a thread are creation rejections.
func (b *backend) fork(ctx context.Context, src, messageID string) (*platforms.CreateSessionResponse, error) {
	c, gen, err := b.conn(ctx)
	if err != nil {
		return nil, errors.Join(platforms.ErrCreationRejected, err)
	}
	params := codexappserver.ThreadForkParams{ThreadID: src, ExcludeTurns: true}
	var resp *codexappserver.ThreadResponse
	if messageID != "" {
		last, err := b.turnBefore(ctx, c, src, messageID)
		if err != nil {
			return nil, errors.Join(platforms.ErrCreationRejected, err)
		}
		if last == "" {
			th, err := c.ReadThread(ctx, src, false)
			if err != nil {
				return nil, errors.Join(platforms.ErrCreationRejected, notFound(err))
			}
			resp, err = c.StartThread(ctx, codexappserver.ThreadStartParams{Cwd: th.Cwd, Model: th.Model})
			if err != nil {
				return nil, err
			}
		}
		params.LastTurnID = last
	}
	if resp == nil {
		if resp, err = c.ForkThread(ctx, params); err != nil {
			return nil, notFound(err)
		}
	}
	id := resp.Thread.ID
	b.mu.Lock()
	if b.gen == gen {
		b.loaded[id] = true
	}
	b.sessionLocked(id).dir = resp.Thread.Cwd
	b.mu.Unlock()
	return &platforms.CreateSessionResponse{ID: id}, nil
}

// turnBefore names the turn preceding the one holding messageID, "" when
// that is the first turn. Message ids are the converter's: msg_<item id>,
// or msg_<turn id> for a failed turn's reply.
func (b *backend) turnBefore(ctx context.Context, c *codexappserver.Client, src, messageID string) (string, error) {
	items, err := c.ListItems(ctx, src)
	if err != nil {
		return "", notFound(err)
	}
	turn := ""
	for _, e := range items {
		if "msg_"+e.Item.ID == messageID || "msg_"+e.TurnID == messageID {
			turn = e.TurnID
			break
		}
	}
	turns, err := c.ListTurns(ctx, src, "notLoaded")
	if err != nil {
		return "", notFound(err)
	}
	for i, t := range turns {
		if t.ID == turn || (turn == "" && "msg_"+t.ID == messageID) {
			if i == 0 {
				return "", nil
			}
			return turns[i-1].ID, nil
		}
	}
	return "", fmt.Errorf("%w: message %s is not in thread %s", platforms.ErrNotFound, messageID, src)
}
