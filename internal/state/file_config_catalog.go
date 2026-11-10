package state

import "strings"

// The allowlist separates deployment/user configuration from generated keys,
// per-session policy snapshots, checkpoints and other runtime settings. Dedicated
// feature tables are not part of this first migration.
var fileSettingKeys = map[string]bool{
	"platforms.enabled":             true,
	"platform.default":              true,
	"session.default_agent":         true,
	"session.archive_resurface":     true,
	"worktree.inherit_permissions":  true,
	"judge_model":                   true,
	"judge_endpoint":                true,
	"judge_delay":                   true,
	"prompt_sections":               true,
	"auto_archive":                  true,
	"model_fallthrough":             true,
	"notifications.badge":           true,
	"projects.idle_timeout_minutes": true,
	"sharing_enabled":               true,
	"beta.factory":                  true,
	"pr_prompt_template":            true,
	"issue_prompt_template":         true,
	"review_prompt_template":        true,
	"link_preview_rules":            true,
	"preview_apps":                  true,
	"webhook.relay_url":             true,
	"webhook.enrollment_token":      true,
}

func isFileSetting(key string) bool {
	return fileSettingKeys[key] || strings.HasPrefix(key, "project:") || strings.HasPrefix(key, "project-defaults:")
}

func isFileSecret(key string) bool {
	return key == "preview_apps" || key == "webhook.enrollment_token"
}
