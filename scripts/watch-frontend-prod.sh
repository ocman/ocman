#!/bin/bash
# Vite owns rebuild watching; type errors remain visible without stopping it.
set -u
cd "$(dirname "$0")/../frontend" || exit 1

pnpm exec tsc -b --watch --preserveWatchOutput &
typecheck=$!
trap 'kill "$typecheck" 2>/dev/null; wait "$typecheck" 2>/dev/null' EXIT
trap 'exit 0' INT TERM

pnpm exec vite build --watch
