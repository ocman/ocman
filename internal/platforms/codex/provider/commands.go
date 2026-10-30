package codexprovider

import (
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

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
