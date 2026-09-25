package term

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// termWindow is a dedicated terminal window with a display title derived
// from what's running in it. Alias of hostsvc.TermWindow so the local
// Host deps and the REST handlers share one shape.
type termWindow = hostsvc.TermWindow

// idleShells are pane_current_command values that mean "nothing
// interesting is running" — an idle shell prompt. Used to decide when a
// command name is worth showing as a tab title.
var idleShells = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true,
	"dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true,
}

// listTermWindowInfo returns the terminal windows for dir (ascending
// index order), each with a display title: the program-set pane title
// (OSC) when meaningful, else the running command when not an idle
// shell, else empty (the UI falls back to the tab number).
func listTermWindowInfo(ctx context.Context, dir string) ([]termWindow, error) {
	out, err := tmux.Output(ctx, "-L", socketName, "list-windows", "-t", SessionName,
		"-F", "#{window_name}\t#{pane_current_command}\t#{pane_title}")
	if err != nil {
		return nil, err
	}
	prefix := WindowPrefix(dir)
	var wins []termWindow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		name := parts[0]
		if !strings.HasPrefix(name, prefix) || !termWindowRe.MatchString(name) {
			continue
		}
		cmd, paneTitle := "", ""
		if len(parts) > 1 {
			cmd = parts[1]
		}
		if len(parts) > 2 {
			paneTitle = parts[2]
		}
		wins = append(wins, termWindow{Name: name, Title: termWindowTitle(cmd, paneTitle)})
	}
	sort.Slice(wins, func(i, j int) bool {
		return termWindowIndex(wins[i].Name) < termWindowIndex(wins[j].Name)
	})
	return wins, nil
}

// termWindowTitle picks a display title from the pane's running command
// and program-set pane title.
func termWindowTitle(cmd, paneTitle string) string {
	pt := strings.TrimSpace(paneTitle)
	if pt != "" && pt != cmd && !looksLikeHostname(pt) {
		return pt
	}
	if cmd != "" && !idleShells[cmd] {
		return cmd
	}
	return ""
}

// looksLikeHostname is a cheap heuristic to reject tmux's default
// pane_title (the local hostname) so it isn't shown as a tab title.
func looksLikeHostname(s string) bool {
	if strings.ContainsAny(s, " /\\:") {
		return false
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		if s == host || strings.HasPrefix(host, s) || strings.HasPrefix(s, host) {
			return true
		}
		if short, _, ok := strings.Cut(host, "."); ok && s == short {
			return true
		}
	}
	return false
}
