package platformsettings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestReloadDrainsAndFencesNewWork(t *testing.T) {
	store := statetest.Open(t)
	m := New(store)
	idle, reloads := false, 0
	var readErr error
	m.Configure(func() { reloads++ }, []string{"opencode", "claudecode"}, false, func(context.Context) (bool, error) { return idle, readErr })
	if m.tryReload(t.Context()) {
		t.Fatal("unscheduled reload")
	}
	finish, err := m.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string, code int) {
		t.Helper()
		w := httptest.NewRecorder()
		m.settings(w, httptest.NewRequest(http.MethodPost, "/api/settings/platforms", strings.NewReader(body)))
		if w.Code != code {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
	}
	post(`{"enabled":["claudecode"]}`, 200)
	if _, err := m.Begin(t.Context()); !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("new turn: %v", err)
	}
	queuedFinish, err := m.Begin(DrainContext(t.Context()))
	if err != nil {
		t.Fatalf("existing queue rejected: %v", err)
	}
	idle = true
	if m.tryReload(t.Context()) {
		t.Fatal("reloaded during admitted work")
	}
	finish()
	queuedFinish()
	idle = false
	if m.tryReload(t.Context()) {
		t.Fatal("reloaded during active turn")
	}
	idle, readErr = true, errors.New("cannot read status")
	if m.tryReload(t.Context()) {
		t.Fatal("uncertain status allowed reload")
	}
	post(`{"enabled":["opencode","claudecode"]}`, 200)
	if m.pending {
		t.Fatal("restoring current selection did not cancel reload")
	}
	post(`{"enabled":["claudecode"]}`, 200)
	readErr = nil
	if !m.tryReload(t.Context()) || reloads != 1 {
		t.Fatalf("reloads: %d", reloads)
	}
	m.Configure(nil, nil, true, nil)
	post(`{"enabled":["opencode"]}`, 409)
	if m.tryReload(t.Context()) {
		t.Fatal("reload without callback")
	}
}

func TestReloadWorker(t *testing.T) {
	m := New(nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	m = New(nil)
	reloaded := make(chan struct{})
	m.Configure(func() { close(reloaded) }, nil, false, func(context.Context) (bool, error) { return true, nil })
	m.pending = true
	go m.Run(t.Context())
	select {
	case <-reloaded:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reload")
	}
}

// The idle check runs without the lock: sends are admitted meanwhile, and
// one admitted during the check defers the reload to the next tick.
func TestIdleCheckDoesNotHoldTheLock(t *testing.T) {
	m := New(nil)
	checking, release := make(chan struct{}), make(chan struct{})
	reloaded := false
	m.Configure(func() { reloaded = true }, nil, false, func(context.Context) (bool, error) {
		close(checking)
		<-release
		return true, nil
	})
	m.pending = true
	result := make(chan bool, 1)
	go func() { result <- m.tryReload(t.Context()) }()
	<-checking
	begun := make(chan func(), 1)
	go func() {
		done, err := m.Begin(DrainContext(t.Context()))
		if err != nil {
			t.Error(err)
		}
		begun <- done
	}()
	select {
	case done := <-begun:
		done()
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Begin waited for the idle check")
	}
	close(release)
	if <-result || reloaded {
		t.Fatal("reloaded over work admitted during the idle check")
	}
}
