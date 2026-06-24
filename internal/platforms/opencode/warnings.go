package opencode

import "github.com/NoUseFreak/ocman/internal/platforms"

const duplicateOpenCodeServersWarningKind = "duplicate_opencode_servers"

func sessionWarningsForDirectory(directory string) []platforms.SessionWarning {
	ports := discoverDuplicateOpenCodeServerPorts(directory)
	if len(ports) < 2 {
		return nil
	}
	return []platforms.SessionWarning{{
		Kind:    duplicateOpenCodeServersWarningKind,
		Message: "Multiple OpenCode servers are running for this project. Live updates may be unreliable for activity started outside this tab.",
		Ports:   ports,
	}}
}
