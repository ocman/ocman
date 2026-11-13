package codexprovider

import (
	"slices"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

type advertisedCommands struct {
	sequence uint64
	list     []platforms.SlashCommandEntry
}

func (a *acpExecution) observeCommands(id string, commands []acp.AvailableCommand) {
	list := []platforms.SlashCommandEntry{}
	seen := map[string]bool{}
	for _, command := range commands {
		name := strings.TrimPrefix(command.Name, "/")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		list = append(list, platforms.SlashCommandEntry{Name: name, Description: command.Description, Source: "command"})
	}
	a.mu.Lock()
	a.commandSeq++
	a.commands[id] = advertisedCommands{sequence: a.commandSeq, list: list}
	a.mu.Unlock()
}

// directoryCommands only reads notifications already received from execution.
// It never starts ACP, creates a session or resumes a native writer for browsing.
// Notifications may precede the create/resume response that records the directory.
func (a *acpExecution) directoryCommands(dir string) []platforms.SlashCommandEntry {
	a.b.mu.Lock()
	var ids []string
	for id, s := range a.b.sessions {
		if s.dir == dir {
			ids = append(ids, id)
		}
	}
	a.b.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	select {
	case <-a.client.Done():
		return nil
	default:
	}
	var latest advertisedCommands
	for _, id := range ids {
		if commands := a.commands[id]; a.loaded[id] && commands.sequence > latest.sequence {
			latest = commands
		}
	}
	return slices.Clone(latest.list)
}

var builtinCommands = []platforms.SlashCommandEntry{
	{Name: "review", Description: "Review uncommitted changes, or follow the given review instructions", Source: "command"},
	{Name: "compact", Description: "Summarize the conversation to free context", Source: "command"},
}

// Commands lists the built-in commands and enabled skills, each name once.
func Commands(entries []codexappserver.SkillsEntry) []platforms.SlashCommandEntry {
	out := append([]platforms.SlashCommandEntry{}, builtinCommands...)
	seen := map[string]bool{"review": true, "compact": true}
	for _, entry := range entries {
		for _, skill := range entry.Skills {
			if !skill.Enabled || seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			description := skill.ShortDescription
			if description == "" {
				description = skill.Description
			}
			out = append(out, platforms.SlashCommandEntry{Name: skill.Name, Description: description, Source: "skill"})
		}
	}
	return out
}
