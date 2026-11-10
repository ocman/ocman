package app

import (
	"flag"
	"os"
	"runtime"
	"strconv"

	"github.com/ocman/ocman/internal/gui"
)

// setupFailedConfigDiagnostics handles errors before normal flag parsing. It
// honors actual CLI overrides without relying on a successfully loaded config.
func setupFailedConfigDiagnostics(fs *flag.FlagSet, args []string) {
	guiMode, logFile := false, ""
	if f := fs.Lookup("gui"); f != nil {
		guiMode, _ = strconv.ParseBool(f.Value.String())
	}
	if f := fs.Lookup("log-file"); f != nil {
		logFile = f.Value.String()
	}
	if value, ok := os.LookupEnv("OCMAN_GUI"); ok {
		if enabled, err := strconv.ParseBool(value); err == nil {
			guiMode = enabled
		}
	}
	if arguments, err := parsedFlagArguments(fs, args); err == nil {
		if value, ok := arguments["gui"]; ok {
			if enabled, err := strconv.ParseBool(value); err == nil {
				guiMode = enabled
			}
		}
		if value, ok := arguments["log-file"]; ok {
			logFile = value
		}
	}
	home, _ := os.UserHomeDir()
	path := setupLogFile(resolveLogPath(logFile, runtime.GOOS, home, os.Getenv("XDG_STATE_HOME")))
	gui.SetFatalContext(guiMode, path)
}
