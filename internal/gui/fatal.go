package gui

import (
	"fmt"
	"os/exec"
	"runtime"

	log "github.com/sirupsen/logrus"
)

// fatalGUI and fatalLogPath are set once by SetFatalContext, before any
// Fatalf call, from main's flag parsing.
var (
	fatalGUI     bool
	fatalLogPath string
)

// Version is the build version shown in the macOS About panel; main sets it.
var Version = "dev"

// runDialog executes the dialog command; a seam so tests never shell out.
var runDialog = func(argv []string) error { return exec.Command(argv[0], argv[1:]...).Run() }

// SetFatalContext configures Fatalf: in GUI mode it also shows a native
// alert, and the message points at logPath when non-empty.
func SetFatalContext(guiMode bool, logPath string) {
	fatalGUI, fatalLogPath = guiMode, logPath
}

// Fatalf logs the error and exits 1, like log.Fatalf. In GUI mode it first
// shows a blocking native alert so the app doesn't vanish silently.
func Fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.StandardLogger().Log(log.FatalLevel, msg)
	if fatalGUI {
		if argv := dialogCommand(runtime.GOOS, dialogMessage(msg, fatalLogPath), exec.LookPath); argv != nil {
			if err := runDialog(argv); err != nil {
				log.WithError(err).Warn("gui: cannot show error dialog")
			}
		}
	}
	log.StandardLogger().Exit(1)
}

func dialogMessage(msg, logPath string) string {
	if logPath == "" {
		return msg
	}
	return msg + "\n\nDetails: " + logPath
}

// dialogCommand returns the argv showing msg as a blocking error alert, or
// nil when no dialog tool is available (stderr already has the message).
// The text is passed as an argument, never spliced into a script.
func dialogCommand(goos, msg string, lookPath func(string) (string, error)) []string {
	switch goos {
	case "darwin":
		return []string{"osascript",
			"-e", "on run argv",
			"-e", `display dialog (item 1 of argv) with title "ocman" buttons {"OK"} default button "OK" with icon stop`,
			"-e", "end run", msg}
	case "linux":
		if _, err := lookPath("zenity"); err == nil {
			return []string{"zenity", "--error", "--no-markup", "--title=ocman", "--text=" + msg}
		}
	}
	return nil
}
