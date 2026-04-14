package main

import (
	"flag"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
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

	srv := server.New(database, stateDB, *addr)
	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
