package app

import (
	"fmt"

	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	opencodeplatform "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/server"
	log "github.com/sirupsen/logrus"
)

// installedOpenCodeMajor is a seam so tests never run the real binary.
var installedOpenCodeMajor = ocv2.InstalledMajor

// requireOpenCodeV2 disables an enabled OpenCode platform whose installed
// binary is older than v2, as if it were omitted from -platforms, and
// returns the issue the doctor reports.
func requireOpenCodeV2(enabled map[string]bool) *server.StartupIssue {
	id := string(opencodeplatform.PlatformID)
	if !enabled[id] {
		return nil
	}
	issue := openCodeVersionIssue(installedOpenCodeMajor())
	if issue == nil {
		return nil
	}
	log.Errorf("%s; starting without the OpenCode platform. %s", issue.Message, issue.Hint)
	delete(enabled, id)
	return issue
}

// openCodeVersionIssue refuses an installed OpenCode older than v2. A
// missing binary or unreadable version is left to the doctor's binary
// check, so saved history stays readable.
func openCodeVersionIssue(major int, ok bool) *server.StartupIssue {
	if !ok || major >= 2 {
		return nil
	}
	return &server.StartupIssue{
		ID:      server.IssueOpenCodeUnsupported,
		Message: fmt.Sprintf("OpenCode v%d is installed, but ocman requires OpenCode v2 or later", major),
		Hint:    "Upgrade OpenCode to v2 (https://opencode.ai), then restart ocman.",
	}
}
