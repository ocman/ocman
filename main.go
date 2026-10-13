package main

import (
	_ "embed"

	"github.com/NoUseFreak/ocman/internal/app"
)

// version is overridden at build time via -ldflags='-X main.version=...'.
var version = "dev"

//go:embed .opencode/skills/ocman-factory/SKILL.md
var factorySkill []byte

//go:embed .opencode/skills/ocman-routines/SKILL.md
var routinesSkill []byte

//go:embed .opencode/skills/ocman-sessions/SKILL.md
var sessionsSkill []byte

//go:embed .opencode/skills/ocman-inbox/SKILL.md
var inboxSkill []byte

//go:embed .opencode/skills/ocman-artifacts/SKILL.md
var artifactsSkill []byte

func main() {
	app.Run(version, map[string][]byte{
		"ocman-factory": factorySkill, "ocman-routines": routinesSkill,
		"ocman-sessions": sessionsSkill, "ocman-inbox": inboxSkill,
		"ocman-artifacts": artifactsSkill,
	})
}
