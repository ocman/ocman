#!/bin/bash
# Watch frontend source files and rebuild on changes
# Used by dev-prod-watch to enable hot reload with production builds

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/../frontend"

echo "[Frontend Watcher] Starting production build watcher..."
echo "[Frontend Watcher] Watching: $(pwd)/src"
echo "[Frontend Watcher] Any file change will trigger rebuild"
echo ""

# Initial build
pnpm build

# Use fswatch if available, otherwise fall back to basic polling
if command -v fswatch &>/dev/null; then
	echo "[Frontend Watcher] Using fswatch for file monitoring"
	fswatch -o src/ | while read -r; do
		echo "[Frontend Watcher] Change detected, rebuilding..."
		pnpm build
		echo "[Frontend Watcher] Build complete"
	done
else
	echo "[Frontend Watcher] fswatch not found, using polling (install with: brew install fswatch)"
	echo "[Frontend Watcher] Run 'cd frontend && pnpm build' manually after changes"

	# Simple polling fallback
	prev_checksum=""
	while true; do
		sleep 2
		current_checksum=$(find src -type f \( -name "*.ts" -o -name "*.tsx" -o -name "*.css" \) | xargs md5 2>/dev/null | md5)
		if [ "$current_checksum" != "$prev_checksum" ] && [ -n "$prev_checksum" ]; then
			echo "[Frontend Watcher] Change detected, rebuilding..."
			pnpm build
			echo "[Frontend Watcher] Build complete - refresh your browser"
			echo ""
		fi
		prev_checksum="$current_checksum"
	done
fi
