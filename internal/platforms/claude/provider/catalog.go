package claudeprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
)

const commandsTTL = 10 * time.Minute

// initCommand is one entry of initialize's `commands`. Its shape was not
// captured (spike §5 lists the key only); the Agent SDK documents
// {name, description, argumentHint}. A bare string is read as a name.
type initCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (c *initCommand) UnmarshalJSON(b []byte) error {
	if json.Unmarshal(b, &c.Name) == nil {
		return nil
	}
	type plain initCommand
	return json.Unmarshal(b, (*plain)(c))
}

// commands is the per-directory command catalog the CLI itself reports: its
// initialize response run in that directory with ocman's plugin, cached.
// Skills are the names a session's system/init listed under `skills`.
type commands struct {
	cli, plugin, config string
	env                 func() []string
	slots               string     // Options.SlotDir: a probe takes a helper slot
	probe               sync.Mutex // held across a CLI call: one at a time
	mu                  sync.Mutex
	dirs                map[string]cachedCommands
	skills              map[string]map[string]bool
}

type cachedCommands struct {
	at     time.Time
	list   []initCommand
	agents []initAgent
	models []initModel
}

// initModel is the CLI's ModelInfo, including its model-specific effort levels.
type initModel struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

func newCommands(opts Options) *commands {
	return &commands{cli: opts.CLI, plugin: opts.PluginDir, config: opts.ConfigDir, slots: opts.SlotDir, env: opts.childEnv, dirs: map[string]cachedCommands{}, skills: map[string]map[string]bool{}}
}

func (c *commands) list(ctx context.Context, dir string) (cachedCommands, error) {
	c.probe.Lock()
	defer c.probe.Unlock()
	c.mu.Lock()
	cached, ok := c.dirs[dir]
	c.mu.Unlock()
	if ok && time.Since(cached.at) < commandsTTL {
		return cached, nil
	}
	var args []string
	if c.plugin != "" {
		args = append(args, "--plugin-dir", c.plugin)
	}
	res, err := initialize(ctx, c.slots, c.cli, dir, c.env(), args...)
	if err != nil {
		return cachedCommands{}, err
	}
	cached = cachedCommands{at: time.Now(), list: res.Commands, agents: res.Agents, models: res.Models}
	c.mu.Lock()
	c.dirs[dir] = cached
	c.mu.Unlock()
	return cached, nil
}

func (c *commands) observeSkills(dir string, skills []string) {
	if len(skills) == 0 {
		return
	}
	set := map[string]bool{}
	for _, s := range skills {
		set[strings.TrimPrefix(s, "/")] = true
	}
	c.mu.Lock()
	c.skills[dir] = set
	c.mu.Unlock()
}

// isSkill reports a skill a session's init named, or one on disk: ocman's
// plugin skills (as <plugin>:<skill>), the user's and the project's. The
// disk check makes the catalog right before any session in dir started.
func (c *commands) isSkill(dir, name string) bool {
	c.mu.Lock()
	observed := c.skills[dir][name]
	c.mu.Unlock()
	if observed {
		return true
	}
	skill := func(root, n string) bool {
		_, err := os.Stat(filepath.Join(root, "skills", n, "SKILL.md"))
		return root != "" && !strings.ContainsAny(n, "/\\") && n != ".." && err == nil
	}
	if plugin, n, ok := strings.Cut(name, ":"); ok {
		return c.plugin != "" && plugin == filepath.Base(c.plugin) && skill(c.plugin, n)
	}
	return skill(c.config, name) || skill(filepath.Join(dir, ".claude"), name)
}

// catalog offers the CLI's model aliases and the directory's measured
// commands and skills. Without a usable CLI the commands are empty and
// typed /name input is still sent as prompt text.
func (l *live) catalog(ctx context.Context, dir string) platforms.DirectoryCatalog {
	models := &platforms.SessionModelsResponse{HasProviders: true}
	for _, m := range []string{"opus", "sonnet", "haiku"} {
		models.Models = append(models.Models, platforms.SessionModel{Provider: "anthropic", ProviderName: "Anthropic", Model: m, ModelName: m, IsAvailable: true})
	}
	cat := platforms.DirectoryCatalog{Models: models, Commands: []platforms.SlashCommandEntry{}}
	if dir == "" || l.versionErr != nil {
		return cat
	}
	cached, err := l.cmds.list(ctx, dir)
	if err != nil {
		log.WithError(err).WithField("directory", dir).Debug("claude command catalog")
		return cat
	}
	if len(cached.models) > 0 {
		models.Models = nil
		for _, m := range cached.models {
			entry := platforms.SessionModel{Provider: "anthropic", ProviderName: "Anthropic", Model: m.Value,
				ModelName: m.DisplayName, IsAvailable: true, Reasoning: m.SupportedEffortLevels, Description: m.Description}
			models.Models = append(models.Models, entry)
			// Existing transcripts name the resolved model, rather than its
			// alias: listed for lookups only, so the picker shows it once.
			if m.ResolvedModel != "" && m.ResolvedModel != m.Value {
				entry.Model, entry.Hidden = m.ResolvedModel, true
				models.Models = append(models.Models, entry)
			}
		}
	}
	for _, a := range cached.agents {
		if a.Name != "" {
			cat.Agents = append(cat.Agents, platforms.AgentCatalogEntry{Name: a.Name, Description: a.Description, Model: a.Model, Mode: "subagent", Kind: "subagent"})
		}
	}
	for _, c := range cached.list {
		name := strings.TrimPrefix(c.Name, "/")
		if name == "" {
			continue
		}
		source := "command"
		if l.cmds.isSkill(dir, name) {
			source = "skill"
		}
		cat.Commands = append(cat.Commands, platforms.SlashCommandEntry{Name: name, Description: c.Description, Source: source})
	}
	return cat
}

// initAgent is one entry of initialize's `agents`, the subagents the model
// can delegate to. Its shape was not captured (spike §5 lists the key only);
// the Agent SDK documents {name, description, model}. A bare string is a name.
type initAgent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model"`
}

func (a *initAgent) UnmarshalJSON(b []byte) error {
	if json.Unmarshal(b, &a.Name) == nil {
		return nil
	}
	type plain initAgent
	return json.Unmarshal(b, (*plain)(a))
}
