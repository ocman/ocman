#!/usr/bin/env bash
# Self-test for check-css-tokens.sh: it must flag a planted undefined token
# and accept defined, JS-set and commented ones.
set -euo pipefail

guard="$(cd "$(dirname "$0")" && pwd)/check-css-tokens.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/tokens.css" <<'EOF'
:root { --bg: #000; }
.a { color: var(--bg); }
EOF
cat >"$tmp/a.tsx" <<'EOF'
// e.g. `var(--only-in-comment)`
const s = { '--dyn': '1px', width: 'var(--dyn)' };
EOF
"$guard" "$tmp" >/dev/null || { echo "check-css-tokens: false positive" >&2; exit 1; }

echo '.b { color: var(--planted-missing); }' >"$tmp/b.css"
if "$guard" "$tmp" >/dev/null 2>&1; then
	echo "check-css-tokens: missed a planted undefined token" >&2
	exit 1
fi
echo "check-css-tokens self-test: ok"
