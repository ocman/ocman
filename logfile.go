package main

import (
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

// logFileMaxBytes caps the log file: a file larger than this at startup is
// moved to <path>.1 (replacing any previous one) before a fresh file opens.
// ponytail: size checked on open only, a long-running process can grow past
// it; add rotation if single runs ever get that chatty.
const logFileMaxBytes = 10 << 20

// defaultLogPath is ~/Library/Logs/ocman/ocman.log on macOS, otherwise
// $XDG_STATE_HOME/ocman/ocman.log (default ~/.local/state).
func defaultLogPath(goos, home, xdgState string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Logs", "ocman", "ocman.log")
	}
	if xdgState == "" {
		xdgState = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(xdgState, "ocman", "ocman.log")
}

// resolveLogPath maps the -log-file flag to a path: empty means the default,
// "-" or "off" disables the file (stderr only, returns "").
func resolveLogPath(flagValue, goos, home, xdgState string) string {
	switch flagValue {
	case "-", "off":
		return ""
	case "":
		return defaultLogPath(goos, home, xdgState)
	}
	return flagValue
}

// openLogFile creates the directory (0700) and opens path for appending
// (0600), first moving an oversized file aside to path+".1".
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > logFileMaxBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// fileHook writes every entry to a file with a plain (uncolored) formatter,
// while the logger's own output stays colored stderr.
type fileHook struct {
	file      *os.File
	formatter log.Formatter
}

func (h *fileHook) Levels() []log.Level { return log.AllLevels }

func (h *fileHook) Fire(e *log.Entry) error {
	b, err := h.formatter.Format(e)
	if err != nil {
		return err
	}
	_, err = h.file.Write(b)
	return err
}

// setupLogFile adds the log file to the standard logger and returns its
// path ("" when disabled or unavailable). Failure warns and continues.
func setupLogFile(path string) string {
	if path == "" {
		return ""
	}
	f, err := openLogFile(path)
	if err != nil {
		log.WithError(err).WithField("path", path).Warn("cannot open log file; logging to stderr only")
		return ""
	}
	log.AddHook(&fileHook{file: f, formatter: &log.TextFormatter{DisableColors: true, FullTimestamp: true}})
	return path
}
