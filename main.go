package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	claudecodeplatform "github.com/NoUseFreak/ocman/internal/platforms/claudecode"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
)

// knownPlatforms lists the valid values for the -platforms flag.
var knownPlatforms = map[string]bool{
	string(opencodeplatform.PlatformID):   true,
	string(claudecodeplatform.PlatformID): true,
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8228", "listen address")
	dbPath := flag.String("db", db.DefaultDBPath(), "path to opencode.db")
	platformsFlag := flag.String("platforms", "opencode", "comma-separated list of platforms to enable (opencode, claude-code)")
	flag.Parse()

	// Parse and validate the -platforms flag.
	enabledPlatforms := map[string]bool{}
	for _, name := range strings.Split(*platformsFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !knownPlatforms[name] {
			fmt.Fprintf(os.Stderr, "Unknown platform: %q (known: opencode, claude-code)\n", name)
			os.Exit(1)
		}
		enabledPlatforms[name] = true
	}
	if len(enabledPlatforms) == 0 {
		fmt.Fprintf(os.Stderr, "No platforms enabled. Use -platforms with at least one of: opencode, claude-code\n")
		os.Exit(1)
	}

	// Open the OpenCode database only when the opencode platform is enabled.
	var database *db.DB
	if enabledPlatforms[string(opencodeplatform.PlatformID)] {
		if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "OpenCode database not found at: %s\n", *dbPath)
			fmt.Fprintf(os.Stderr, "Make sure OpenCode is installed and has been used at least once.\n")
			os.Exit(1)
		}

		var err error
		database, err = db.Open(*dbPath)
		if err != nil {
			log.Fatalf("Failed to open database: %v", err)
		}
		defer database.Close()
	}

	stateDB, err := state.Open(state.DefaultDBPath())
	if err != nil {
		log.Fatalf("Failed to open state database: %v", err)
	}
	defer stateDB.Close()

	// Pre-warm the pricing table in the background so the first metrics request
	// doesn't block on a remote fetch.
	go pricing.Load()

	// Create a context that is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Register only the platform adapters requested via -platforms.
	registry := platforms.NewRegistry()
	if enabledPlatforms[string(opencodeplatform.PlatformID)] {
		registry.Register(opencodeplatform.New(database))
	}
	if enabledPlatforms[string(claudecodeplatform.PlatformID)] {
		registry.Register(claudecodeplatform.New())
	}

	srv := server.New(database, stateDB, *addr, registry)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	log.Info("server stopped gracefully")
}
