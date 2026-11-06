package state

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestPluginListReleasesSQLBeforeRedactionLock(t *testing.T) {
	exporter := recordStateSQL(t)
	d, desc, _ := pluginFixture(t)
	requirePluginOK(t, d.SetPluginHealth(t.Context(), desc.ID, PluginHealth{Status: "unhealthy", LastError: "diagnostic"}))
	exporter.Reset()
	d.pluginMu.Lock()
	unlock := sync.OnceFunc(d.pluginMu.Unlock)
	defer unlock()
	done := make(chan error, 1)
	go func() {
		_, err := d.ListPlugins(t.Context())
		done <- err
	}()
	queried := false
	deadline := time.Now().Add(2 * time.Second)
	for !queried && time.Now().Before(deadline) {
		for _, span := range exporter.GetSpans() {
			queried = queried || span.Name == "sql.conn.query"
		}
		runtime.Gosched()
	}
	if !queried {
		t.Fatal("plugin list did not read registrations")
	}
	// A configuration writer holds pluginMu before requesting this connection.
	// Listing must release it before redaction waits for the same mutex.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var one int
	if err := d.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("listing held SQL while waiting for pluginMu: %v", err)
	}
	unlock()
	select {
	case err := <-done:
		requirePluginOK(t, err)
	case <-ctx.Done():
		t.Fatal("listing did not finish after redaction was released")
	}
}
