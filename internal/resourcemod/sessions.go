package resourcemod

import (
	"context"
	"slices"
	"strings"

	"github.com/ocman/ocman/internal/hostsvc"
)

// sessionArgument only accepts explicit session identity options, not arbitrary
// mentions of IDs in prompts or directory names. Unknown and ambiguous native
// identities deliberately have no link.
func sessionArgument(command string) string {
	args := strings.Fields(command)
	var sessionID, session, resume string
	for i := 1; i < len(args); i++ {
		flag, value, equal := strings.Cut(args[i], "=")
		if flag != "--session-id" && flag != "--resume" && flag != "--session" {
			continue
		}
		if !equal && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			value = args[i]
		}
		switch flag {
		case "--session-id":
			sessionID = value
		case "--session":
			session = value
		case "--resume":
			resume = value
		}
	}
	// Forks resume a source but explicitly name a new session. Never link the
	// source instead of that new identity, regardless of argument order.
	if sessionID != "" {
		return sessionID
	}
	if session != "" {
		return session
	}
	return resume
}

func (m *Module) sessionLinks(ctx context.Context, owner string, processes []hostsvc.ResourceProcess) []hostsvc.ResourceProcess {
	byPID := make(map[int]hostsvc.ResourceProcess, len(processes))
	candidates := make(map[string]string)
	for _, process := range processes {
		byPID[process.PID] = process
		if id := sessionArgument(process.Command); id != "" {
			candidates[id] = ""
		}
	}
	if len(candidates) == 0 {
		return processes
	}
	seen := make(map[string]bool)
	for offset := 0; ; offset += 500 {
		rows, err := m.Sessions(ctx, owner, 500, offset)
		if err != nil {
			return processes
		}
		for _, row := range rows {
			if _, relevant := candidates[row.NativeID]; !relevant {
				continue
			}
			if seen[row.NativeID] || row.Purged {
				candidates[row.NativeID] = ""
			} else {
				candidates[row.NativeID] = row.ID
			}
			seen[row.NativeID] = true
		}
		if len(rows) < 500 {
			break
		}
	}
	out := slices.Clone(processes)
	for i, process := range out {
		visited := make(map[int]bool)
		for !visited[process.PID] {
			visited[process.PID] = true
			if id := sessionArgument(process.Command); id != "" {
				out[i].SessionID = candidates[id]
				break
			}
			parent, ok := byPID[process.ParentPID]
			if !ok {
				break
			}
			process = parent
		}
	}
	return out
}
