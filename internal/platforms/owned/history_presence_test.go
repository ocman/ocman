package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type presencePlatform struct {
	*nativePlatform
	check func(string) (bool, error)
}

func (p *presencePlatform) HistorySessionExists(_ context.Context, id string) (bool, error) {
	return p.check(id)
}

func TestHistoryPresenceUsesBoundNativeIdentity(t *testing.T) {
	a, p, _ := fixture(t)
	row, err := a.historyIdentity(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.HistorySessionExists(t.Context(), row.ID); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported source = %v", err)
	}
	var present bool
	var sourceErr error
	var replaceSource bool
	a.native = &presencePlatform{nativePlatform: p, check: func(id string) (bool, error) {
		if id != "native" {
			t.Fatalf("point lookup received %q, want native ID", id)
		}
		if replaceSource {
			p.source = "another-source"
		}
		return present, sourceErr
	}}
	for _, present = range []bool{true, false} {
		if got, err := a.HistorySessionExists(t.Context(), row.ID); err != nil || got != present {
			t.Fatalf("presence = %v, %v; want %v", got, err, present)
		}
	}
	const unknown = "ocm_00000000-0000-4000-8000-000000000099"
	if _, err := a.HistorySessionExists(t.Context(), unknown); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unknown binding should fall back to inventory: %v", err)
	}
	if _, err := a.HistorySessionExists(t.Context(), "native"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("native ID bypassed canonical routing: %v", err)
	}
	sourceErr = errors.New("source offline")
	if _, err := a.HistorySessionExists(t.Context(), row.ID); !errors.Is(err, sourceErr) {
		t.Fatalf("source error was not preserved: %v", err)
	}
	sourceErr = nil
	replaceSource = true
	if _, err := a.HistorySessionExists(t.Context(), row.ID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("changed source confirmed absence: %v", err)
	}
}
