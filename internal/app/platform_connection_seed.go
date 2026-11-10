package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/state"
)

// Snapshot before state.Open creates the schema, identity or startup settings.
func existingInstallation(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir() && info.Size() > 0, nil
}

func connectionConfigurations() map[string]hostsvc.ConnectionConfiguration {
	home, _ := os.UserHomeDir()
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	claude := os.Getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	openCode := os.Getenv("OPENCODE_CONFIG_DIR")
	if openCode == "" {
		openCode = filepath.Join(config, "opencode")
	}
	return map[string]hostsvc.ConnectionConfiguration{
		"opencode":   {Label: "OpenCode", ExistingDirectory: openCode, SeparateConfiguration: true, IsolationNote: "Uses explicit native paths and credentials. Readiness requires native inspection."},
		"claudecode": {Label: "Claude Code", ExistingDirectory: claude, SeparateConfiguration: true, IsolationNote: "Uses a dedicated CLAUDE_CONFIG_DIR. Native account inspection must confirm isolation."},
		"codex":      {Label: "Codex", ExistingDirectory: codex, SeparateConfiguration: true, IsolationNote: "Uses a dedicated CODEX_HOME. Native account inspection must confirm isolation."},
	}
}

func initializeConnections(ctx context.Context, db *state.DB, existed bool, enabled map[string]bool, configs map[string]hostsvc.ConnectionConfiguration) (*platformconnections.Store, error) {
	store := platformconnections.New(db)
	seed := platformconnections.Seed{ExistingInstallation: existed}
	if existed {
		kinds := map[string]bool{}
		for kind := range enabled {
			kinds[kind] = true
		}
		scopes, err := db.SessionReferenceScopes(ctx)
		if err != nil {
			return nil, err
		}
		for _, scope := range scopes {
			if _, ok := configs[scope]; ok {
				kinds[scope] = true
			}
		}
		def, err := db.DefaultPlatform(ctx)
		if err != nil {
			return nil, err
		}
		var selected []string
		for kind, on := range enabled {
			if on {
				selected = append(selected, kind)
			}
		}
		def = state.PickEnabledPlatform(def, selected)
		if _, ok := configs[def]; ok {
			kinds[def] = true
			seed.DefaultKind = def
		}
		ordered := make([]string, 0, len(kinds))
		for kind := range kinds {
			ordered = append(ordered, kind)
		}
		sort.Strings(ordered)
		for _, kind := range ordered {
			config := configs[kind]
			seed.Connections = append(seed.Connections, platformconnections.Input{Kind: kind, Name: config.Label, Configuration: "existing", Directory: config.ExistingDirectory, Enabled: enabled[kind]})
		}
	}
	_, err := store.Initialize(ctx, seed)
	return store, err
}
