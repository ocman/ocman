package gui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"runtime"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

func TestDialogMessage(t *testing.T) {
	if got := dialogMessage("boom", ""); got != "boom" {
		t.Errorf("no log path: got %q", got)
	}
	if got, want := dialogMessage("boom", "/tmp/ocman.log"), "boom\n\nDetails: /tmp/ocman.log"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDialogCommand(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/zenity", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }

	mac := dialogCommand("darwin", `say "hi"`, missing)
	if mac[0] != "osascript" || mac[len(mac)-1] != `say "hi"` {
		t.Errorf("darwin: %q (message must be the trailing argv, not script text)", mac)
	}
	if got, want := dialogCommand("linux", "boom", found), []string{"zenity", "--error", "--no-markup", "--title=ocman", "--text=boom"}; !reflect.DeepEqual(got, want) {
		t.Errorf("linux: %q, want %q", got, want)
	}
	if got := dialogCommand("linux", "boom", missing); got != nil {
		t.Errorf("linux without zenity: %q, want nil", got)
	}
	if got := dialogCommand("windows", "boom", found); got != nil {
		t.Errorf("windows: %q, want nil", got)
	}
}

func TestFatalfShowsDialogOnlyInGUIMode(t *testing.T) {
	logger := log.StandardLogger()
	oldExit, oldRun := logger.ExitFunc, runDialog
	t.Cleanup(func() { logger.ExitFunc, runDialog = oldExit, oldRun; SetFatalContext(false, "") })

	var code int
	logger.ExitFunc = func(c int) { code = c }
	var ran []string
	runDialog = func(argv []string) error { ran = argv; return nil }

	SetFatalContext(false, "/tmp/x.log")
	Fatalf("bad %s", "relay")
	if code != 1 || ran != nil {
		t.Fatalf("CLI: exit=%d dialog=%q, want exit 1 and no dialog", code, ran)
	}

	SetFatalContext(true, "/tmp/x.log")
	Fatalf("bad %s", "relay")
	if want := dialogCommand(runtime.GOOS, "bad relay\n\nDetails: /tmp/x.log", exec.LookPath); !reflect.DeepEqual(ran, want) {
		t.Fatalf("GUI: dialog argv %q, want %q", ran, want)
	}
}

func TestWaitForServer(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ok.Close()
	if err := waitForServer(ok.URL, time.Second); err != nil {
		t.Fatalf("healthy backend: %v", err)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()
	if err := waitForServer(broken.URL, 200*time.Millisecond); err == nil {
		t.Fatal("500 backend must time out")
	}
}
