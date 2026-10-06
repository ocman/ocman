package autoapprove

import (
	"encoding/json"
	"regexp"
)

// ponytail: refresh hints, not shell parsing. Quoting/wrappers can be missed;
// false positives only refetch. Add a shell parser if accuracy becomes necessary.
var gitCommandHint = regexp.MustCompile(`(?:^|[;&|(\n])\s*(?:command\s+)?git\s+(?:(?:-C|-c|--git-dir|--work-tree)\s+\S+\s+)*(commit|push)(?:\s|$)`)

func (t *Tee) dispatchTerminalPart(dataJSON string) {
	if t.OnTerminalPart == nil && t.OnGitCommand == nil {
		return
	}
	type toolState struct {
		Status   string `json:"status"`
		Output   string `json:"output"`
		Metadata struct {
			Output string `json:"output"`
		} `json:"metadata"`
		Input struct {
			Command string `json:"command"`
		} `json:"input"`
	}
	type part struct {
		ID        string    `json:"id"`
		MessageID string    `json:"messageID"`
		SessionID string    `json:"sessionID"`
		CallID    string    `json:"callID"`
		Type      string    `json:"type"`
		Tool      string    `json:"tool"`
		State     toolState `json:"state"`
	}
	type holder struct {
		Part *part `json:"part"`
	}
	var envelope struct {
		Properties *holder `json:"properties"`
		Data       *holder `json:"data"`
		Part       *part   `json:"part"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &envelope); err != nil {
		return
	}
	var value *part
	switch {
	case envelope.Properties != nil:
		value = envelope.Properties.Part
	case envelope.Data != nil:
		value = envelope.Data.Part
	default:
		value = envelope.Part
	}
	if value == nil {
		var raw part
		if err := json.Unmarshal([]byte(dataJSON), &raw); err == nil {
			value = &raw
		}
	}
	if value == nil || value.Type != "tool" || value.Tool != "bash" ||
		(value.State.Status != "completed" && value.State.Status != "error") ||
		value.SessionID == "" || value.MessageID == "" || value.ID == "" {
		return
	}
	if t.OnGitCommand != nil {
		for _, match := range gitCommandHint.FindAllStringSubmatch(value.State.Input.Command, -1) {
			key := value.SessionID + "\x00" + value.ID + "\x00" + match[1]
			if t.gitCommandHints[key] {
				continue
			}
			// Bound memory on long-lived streams. Re-observations after eviction
			// are harmless refreshes.
			if len(t.gitCommandHints) >= 256 {
				clear(t.gitCommandHints)
			}
			if t.gitCommandHints == nil {
				t.gitCommandHints = make(map[string]bool)
			}
			t.gitCommandHints[key] = true
			t.OnGitCommand(value.SessionID, match[1])
		}
	}
	output := value.State.Output
	if output == "" {
		output = value.State.Metadata.Output
	}
	if output == "" || t.OnTerminalPart == nil {
		return
	}
	t.OnTerminalPart(terminalPart{value.SessionID, value.MessageID, value.ID, value.CallID, value.State.Input.Command, output})
}
