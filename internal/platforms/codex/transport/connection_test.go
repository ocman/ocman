package codextransport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestNamedConnectionPinsHomeAndRuntimeState(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var accounts []*Platform
	for range 2 {
		connection := ConnectionConfig{ID: "cnx_" + uuid.NewString(), Home: t.TempDir()}
		p, err := newPlatform(ctx, Config{DataDir: r.data, Connection: &connection}, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		canonical, err := filepath.EvalSymlinks(connection.Home)
		if err != nil {
			t.Fatal(err)
		}
		if p.home != canonical {
			t.Fatalf("named account borrowed the machine home: %q, want %q", p.home, canonical)
		}
		identity, err := p.HistoryIdentity(ctx)
		if err != nil || identity.SourceKey != canonical || identity.Platform != "codex" {
			t.Fatalf("physical history identity: %+v %v", identity, err)
		}
		accounts = append(accounts, p)
	}
	if accounts[0].root == accounts[1].root || accounts[0].rules == accounts[1].rules {
		t.Fatal("same-kind accounts share supervisor or rules state")
	}
	if os.Getenv("CODEX_HOME") == accounts[0].home || os.Getenv("CODEX_HOME") == accounts[1].home {
		t.Fatal("constructor changed the process-global account")
	}
}

func TestNamedConnectionNeverFallsBack(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		name, id, home, data string
	}{
		{"missing-home", "cnx_" + uuid.NewString(), "", r.data},
		{"relative-home", "cnx_" + uuid.NewString(), "relative", r.data},
		{"missing-id", "", t.TempDir(), r.data},
		{"name-as-id", "Codex Work", t.TempDir(), r.data},
		{"traversal-id", "../other", t.TempDir(), r.data},
		{"missing-data", "cnx_" + uuid.NewString(), t.TempDir(), ""},
		{"relative-data", "cnx_" + uuid.NewString(), t.TempDir(), "relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p, err := newPlatform(ctx, Config{DataDir: tc.data, Connection: &ConnectionConfig{ID: tc.id, Home: tc.home}}, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
			if p != nil {
				p.Close()
			}
			if err == nil || p != nil {
				t.Fatal("invalid named connection fell back to the machine account")
			}
		})
	}
}
