package platforms

// displayNames names the known platforms for hosts that only know a
// platform's id, such as a hub labelling a remote's advertised platforms.
var displayNames = map[ID]string{OpenCodeID: "OpenCode", "claudecode": "Claude Code"}

// DisplayName is the human name of a base platform id, or the id itself.
func DisplayName(id ID) string {
	if name, ok := displayNames[id]; ok {
		return name
	}
	return string(id)
}
