// This test-only executable speaks the provider protocol and never runs claude.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor"
)

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "provider" {
		args = args[2:]
	}
	flags := flag.NewFlagSet("fake", flag.ExitOnError)
	socket := flags.String("socket", "", "")
	spool := flags.String("spool", "", "")
	_ = flags.String("repo", "", "")
	version := flags.Int("version", providerprotocol.CurrentVersion, "")
	_ = flags.Parse(args)
	var s *providersupervisor.Supervisor
	view := make(map[string]providerprotocol.Event)
	backend := providersupervisor.Backend{
		Hello: providerprotocol.Hello{PlatformID: "fake", ProtocolVersion: *version, CLIVersion: "fixture", Capabilities: providerprotocol.Capabilities{Capabilities: platforms.Capabilities{Events: true, RespondPermission: true}}},
		Snapshot: func(_ context.Context, id string) ([]providerprotocol.Event, error) {
			var events []providerprotocol.Event
			for _, event := range view {
				events = append(events, event)
			}
			return events, nil
		},
	}
	backend.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/test/idle" {
			if err := s.Publish(providerprotocol.Event{Type: "session.idle", Properties: json.RawMessage(`{"sessionID":"s"}`)}, nil); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(204)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/test/event" {
			var event providerprotocol.Event
			if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			err := s.Publish(event, func() {
				if event.Type != "message.part.delta" {
					view[event.Type] = event
					return
				}
				var delta struct {
					Delta string `json:"delta"`
				}
				_ = json.Unmarshal(event.Properties, &delta)
				part := view["message.part.updated"]
				var props map[string]any
				_ = json.Unmarshal(part.Properties, &props)
				if nested, ok := props["part"].(map[string]any); ok {
					text, _ := nested["text"].(string)
					nested["text"] = text + delta.Delta
					part.Properties, _ = json.Marshal(props)
					view[part.Type] = part
				}
			})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(204)
			return
		}
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/permissions/") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/permissions/"), "/reply")
			props, _ := json.Marshal(map[string]any{"sessionID": "s", "requestID": id})
			if err := s.Publish(providerprotocol.Event{Type: "permission.replied", Properties: props}, nil); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(204)
			return
		}
		http.NotFound(w, r)
	})
	var err error
	s, err = providersupervisor.New(*spool, backend)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := s.Serve(ctx, *socket); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
