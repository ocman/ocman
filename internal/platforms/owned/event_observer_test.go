package owned

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type nativeEventObserver struct {
	*nativePlatform
	platforms.OpenCodeEventObserver
	seen   string
	prompt platforms.LivePrompt
}

func (p *nativeEventObserver) ObserveSessionStatus(_ string, _ uint64, id, _ string, _ ...*db.SessionNotice) bool {
	p.seen = id
	return true
}

func (p *nativeEventObserver) SessionStatusOnPort(id, _ string) (db.SessionStatus, error) {
	p.seen = id
	return db.StatusDone, nil
}

func (p *nativeEventObserver) ObservePromptAskedFromPort(_ string, _ uint64, _, _ string, prompt platforms.LivePrompt) {
	p.prompt = prompt
}

func (p *nativeEventObserver) ObservePromptResolved(_, _, id, _ string) { p.seen = id }

func (p *nativeEventObserver) StartPromptReconciliation(_ context.Context, _ string, _ []string, callbacks ...func(platforms.LivePrompt)) <-chan bool {
	for _, callback := range callbacks {
		callback(platforms.LivePrompt{"sessionID": "native", "id": "permission", "metadata": map[string]any{"sessionID": "opaque"}})
	}
	done := make(chan bool, 1)
	done <- true
	close(done)
	return done
}

func TestCanonicalEventObserverTranslatesOnlySessionReferences(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	native := &nativeEventObserver{nativePlatform: p}
	a.native = native
	observer := platforms.OpenCodeObserver(a)
	if observer == nil {
		t.Fatal("canonical adapter lost live event observer")
	}
	if !observer.ObserveSessionStatus("port", 7, id, "busy") || native.seen != "native" {
		t.Fatalf("status not translated: %s", native.seen)
	}
	native.seen = ""
	if observer.ObserveSessionStatus("port", 7, "native", "busy") || native.seen != "" {
		t.Fatal("raw ID reached native status cache")
	}
	// The settled status is the stored summary with live observations, not a
	// native database read.
	native.seen = ""
	summary, err := a.SessionSummary(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := observer.SessionStatusOnPort(id, "port"); err != nil || status != summary.Status || native.seen != "" {
		t.Fatalf("status read: %s %v (native %q)", status, err, native.seen)
	}
	if _, err := observer.SessionStatusOnPort("native", "port"); err == nil {
		t.Fatal("raw ID status read accepted")
	}
	prompt := platforms.LivePrompt{"sessionID": id, "id": "permission", "metadata": map[string]any{"sessionID": "opaque"}}
	observer.ObservePromptAskedFromPort("port", 7, "/repo", "permission", prompt)
	if native.prompt["sessionID"] != "native" || prompt["sessionID"] != id || native.prompt["metadata"].(map[string]any)["sessionID"] != "opaque" {
		t.Fatalf("prompt translation mutated content: %+v", native.prompt)
	}
	observer.ObservePromptResolved("/repo", "permission", id, "permission")
	if native.seen != "native" {
		t.Fatal("resolution not translated")
	}
	var received platforms.LivePrompt
	if !<-observer.StartPromptReconciliation(t.Context(), "port", []string{"/repo"}, func(prompt platforms.LivePrompt) { received = prompt }) || received["sessionID"] != id || received["id"] != "permission" || received["metadata"].(map[string]any)["sessionID"] != "opaque" {
		t.Fatalf("snapshot callback exposed native ID: %+v", received)
	}
}
