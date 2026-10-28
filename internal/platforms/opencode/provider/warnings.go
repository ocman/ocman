package opencode

import "github.com/ocman/ocman/internal/platforms"

const duplicateOpenCodeServersWarningKind = "duplicate_opencode_servers"

func (d *Discovery) sessionWarningsForDirectory(directory string) []platforms.SessionWarning {
	ports := d.discoverDuplicateOpenCodeServerPorts(directory)
	if len(ports) < 2 {
		return nil
	}
	return []platforms.SessionWarning{{
		Kind:    duplicateOpenCodeServersWarningKind,
		Message: "Multiple OpenCode servers are running for this project. Live updates may be unreliable for activity started outside this tab.",
		Ports:   ports,
	}}
}
