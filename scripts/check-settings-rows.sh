#!/usr/bin/env bash
# Fails CI when a settings field is hand-rolled with raw `settings-row`
# markup instead of the <SettingRow>/<SettingToggle>/<SettingNumber>
# components. Those components bake in the save-status indicator (spinner
# while saving, checkmark after), so going through them guarantees every
# new setting gets that feedback — it cannot be forgotten.
#
# The components themselves (SettingRow.tsx) and the settings.css rules
# legitimately reference the class names, so they're excluded.
#
# If you truly need custom markup for a non-standard widget, suppress with
# an explicit `{/* ocman:allow-raw-setting */}` on the same line.
#
# Exit code: 0 on clean, 1 on violations.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

matches=$(
	rg -n --no-messages \
		--glob 'frontend/src/**/*.tsx' \
		--glob '!**/SettingRow.tsx' \
		--glob '!**/*.test.tsx' \
		"className=[\"']settings-row[\"']" "$ROOT" || true
)

offenders=$(echo "$matches" | grep -v 'ocman:allow-raw-setting' | sed '/^$/d' || true)

if [[ -n "$offenders" ]]; then
	echo "Hand-rolled settings-row markup detected — use <SettingRow> + <SettingToggle>/<SettingNumber> so the save-status indicator is included:" >&2
	echo "$offenders" >&2
	exit 1
fi

echo "check-settings-rows: all settings fields go through <SettingRow>."
exit 0
