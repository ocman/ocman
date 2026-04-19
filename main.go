package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dbPath := flag.String("db", db.DefaultDBPath(), "path to opencode.db")
	flag.Parse()

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "OpenCode database not found at: %s\n", *dbPath)
		fmt.Fprintf(os.Stderr, "Make sure OpenCode is installed and has been used at least once.\n")
		os.Exit(1)
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

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

	// Register all platform adapters. Each adapter knows how to report
	// its own availability; the Claude Code adapter stays silent if
	// Claude Code isn't installed.
	registry := platforms.NewRegistry()
	registry.Register(opencodeplatform.New(database))
	registry.Register(claudecodeplatform.New())

	srv := server.New(database, stateDB, *addr, registry)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	log.Info("server stopped gracefully")
}
