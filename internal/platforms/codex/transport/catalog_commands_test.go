package codextransport

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/testutil"
)

func TestCatalogReadsAdvertisedCommandsWithoutLaunchingExecution(t *testing.T) {
	r := newRig(t)
	p := r.platform(t.Context())
	req := platforms.DirectoryCatalogRequest{Directory: "/repo"}
	before, err := p.LiveDirectoryCatalog(t.Context(), req)
	if err != nil || before == nil {
		t.Fatalf("catalog = %+v %v", before, err)
	}
	if p.current() != nil {
		t.Fatal("catalog launched an execution supervisor")
	}
	if _, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: req.Directory}); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, 5*time.Second, "ACP command catalog", func() bool {
		catalog, err := p.LiveDirectoryCatalog(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		for _, command := range catalog.Commands {
			if command.Name == "status" && command.Description == "Show Codex status" {
				return true
			}
		}
		return false
	})
	other, err := p.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range other.Commands {
		if command.Name == "status" {
			t.Fatal("session commands leaked to another directory")
		}
	}
	r.killSupervisors()
	if catalog, err := p.LiveDirectoryCatalog(t.Context(), req); err != nil || catalog == nil {
		t.Fatalf("dead execution blocked the read-only catalog: %+v %v", catalog, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.LiveDirectoryCatalog(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("catalog ignored cancellation: %v", err)
	}
}

func TestMergeAdvertisedCommands(t *testing.T) {
	base := []platforms.SlashCommandEntry{
		{Name: "review", Source: "command"},
		{Name: "pdf", Description: "PDF tools", Source: "skill"},
		{Name: "docx", Description: "Word tools", Source: "skill"},
	}
	for _, tc := range []struct {
		name string
		live []platforms.SlashCommandEntry
		want []platforms.SlashCommandEntry
	}{
		{"announced", []platforms.SlashCommandEntry{{Name: "status", Source: "command"}, {Name: "pdf", Source: "command"}},
			[]platforms.SlashCommandEntry{{Name: "status", Source: "command"}, base[1], base[2]}},
		{"description", []platforms.SlashCommandEntry{{Name: "pdf", Description: "Live PDF", Source: "command"}},
			[]platforms.SlashCommandEntry{{Name: "pdf", Description: "Live PDF", Source: "skill"}, base[2]}},
		{"empty", []platforms.SlashCommandEntry{}, base[1:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeAdvertisedCommands(base, tc.live); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("merge = %+v, want %+v", got, tc.want)
			}
		})
	}
}
