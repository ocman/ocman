package owned

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type statusEventNative struct {
	*nativeEventObserver
	turn     db.TurnState
	id, port string
}

func (p *statusEventNative) SettleStoredSessionStatus(id, port string, inferred db.SessionStatus) db.SessionStatus {
	p.id, p.port = id, port
	return db.SettleSessionStatus(p.turn, port != "", inferred)
}

func (p *statusEventNative) EnrichStoredSessions(_ context.Context, rows []db.Session, _ map[string]string) error {
	for i := range rows {
		rows[i].Status = p.SettleStoredSessionStatus(rows[i].ID, "port", rows[i].Status)
	}
	return nil
}

func TestEventStatusDoesNotReadAccountingOrInventory(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	a, p, id := storedFixture(t)
	native := &statusEventNative{nativeEventObserver: &nativeEventObserver{nativePlatform: p}}
	a.native = native
	observer := platforms.OpenCodeObserver(a)
	for _, tc := range []struct {
		turn db.TurnState
		want db.SessionStatus
	}{
		{db.TurnRunning, db.StatusBusy}, {db.TurnSettled, db.StatusWaiting}, {db.TurnUnobserved, db.StatusWaiting},
	} {
		native.turn = tc.turn
		exporter.Reset()
		got, err := observer.SessionStatusOnPort(id, "port")
		if err != nil || got != tc.want {
			t.Fatalf("turn %v: got %s, want %s: %v", tc.turn, got, tc.want, err)
		}
		if native.id != "native" || native.port != "port" || native.seen != "" {
			t.Fatalf("status used wrong identity/port or native DB: %+v", native)
		}
		for _, span := range exporter.GetSpans() {
			for _, attr := range span.Attributes {
				if string(attr.Key) == "db.query.text" {
					query := attr.Value.AsString()
					if strings.Contains(query, "analytics_") || strings.Contains(query, "SELECT id,parent_id") {
						t.Errorf("tiny status lookup performed accounting/inventory: %s", query)
					}
				}
			}
		}
	}
	// A retained physical source must never borrow the current source's turn.
	p.source = "replacement"
	native.turn, native.id = db.TurnRunning, ""
	if got, err := observer.SessionStatusOnPort(id, "port"); err != nil || got != db.StatusWaiting || native.id != "" {
		t.Fatalf("replacement source leaked status: %s %v", got, err)
	}
	a.source.SourceKey = "replacement"
	if got, err := observer.SessionStatusOnPort(id, "port"); err != nil || got != db.StatusWaiting || native.id != "" {
		t.Fatalf("retained physical source borrowed current status: %s %v", got, err)
	}
	a.unavailable = errors.New("offline")
	if got, err := observer.SessionStatusOnPort(id, "port"); err != nil || got != db.StatusWaiting {
		t.Fatalf("offline history lost: %s %v", got, err)
	}
	if _, err := observer.SessionStatusOnPort("native", "port"); err == nil {
		t.Fatal("raw native reference accepted")
	}
	a.owner = "other-owner"
	if _, err := observer.SessionStatusOnPort(id, "port"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("another owner supplied status: %v", err)
	}
}

func TestEventStatusFailsClosedWithoutPersistedHistory(t *testing.T) {
	a, p, _ := fixture(t)
	identity, err := a.historyIdentity(t.Context(), "not-ingested")
	if err != nil {
		t.Fatal(err)
	}
	a.native = &nativeEventObserver{nativePlatform: p}
	observer := platforms.OpenCodeObserver(a)
	if _, err := observer.SessionStatusOnPort(identity.ID, "port"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing history status: %v", err)
	}
	_ = a.history.Close()
	if _, err := observer.SessionStatusOnPort(identity.ID, "port"); err == nil {
		t.Fatal("closed history accepted")
	}
}
