package module

import "strings"

// Titles describe the work rather than implementation-specific loop names.
func ScheduleTitle(name string) string {
	titles := map[string]string{
		"worktree-cleanup":              "Clean archived worktrees",
		"auto-archive":                  "Archive inactive sessions and projects",
		"composer-attachment-sweep":     "Remove expired composer attachments",
		"snooze":                        "Wake snoozed sessions",
		"projects-index":                "Refresh the project index",
		"pr-watch":                      "Check watched pull requests",
		"routines":                      "Check scheduled routines",
		"idle-projects":                 "Check idle project runtimes",
		"queue-sweep":                   "Deliver queued session messages",
		"autoapprove-watcher":           "Check pending agent approvals",
		"web-push":                      "Check notification candidates",
		"web-push-delivery":             "Deliver web notifications",
		"inbox-reconcile":               "Reconcile inbox permissions",
		"webhook-history":               "Remove expired webhook deliveries",
		"session-reference-recovery":    "Recover disconnected session references",
		"platform-reload":               "Check pending platform reloads",
		"database-size":                 "Sample database sizes",
		"subscription-usage":            "Refresh subscription usage history",
		"llm-metrics":                   "Collect language model metrics",
		"stats-metrics":                 "Refresh usage metrics",
		"plugin-conversation-reconcile": "Reconcile plugin conversation replies",
		"plugin-session-reconcile":      "Reconcile plugin session completions",
		"managed-runtime-health":        "Check managed runtime health",
		"machine-supervisor":            "Check the machine runtime",
		"opencode-version":              "Check the installed OpenCode version",
		"remote-project-inventory":      "Refresh remote project inventories",
		"plugin-conversation-delivery":  "Check pending plugin conversation deliveries",
		"plugin-session-delivery":       "Check pending plugin session deliveries",
		"plugin-conversation-prune":     "Prune plugin conversation delivery history",
		"plugin-session-prune":          "Prune plugin session delivery history",
	}
	if title := titles[name]; title != "" {
		return title
	}
	return "Check " + strings.NewReplacer("-", " ", ".", " ").Replace(name)
}
