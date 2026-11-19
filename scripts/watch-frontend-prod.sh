#!/bin/bash
# Vite owns rebuild watching; type errors remain visible without stopping it.
#
# The type checker is tsc, not tsrs: tsrs watch mode keeps most of each full
# rebuild's memory (0.5-1 GB on this project for a branch switch or rebase), so
# a long session grows to tens of GB. Builds and CI still use tsrs.
#
# Both watchers run in the background and the script blocks in `wait`, which a
# trapped signal interrupts. A trap set while a foreground command runs would
# wait for that command to exit, so SIGTERM would never stop the script.
set -u
cd "$(dirname "$0")/../frontend" || exit 1

typecheck=""
vite=""
trap 'kill $typecheck $vite 2>/dev/null; wait 2>/dev/null' EXIT
trap 'exit 0' INT TERM HUP

pnpm exec tsc -b --watch --preserveWatchOutput &
typecheck=$!
pnpm exec vite build --watch &
vite=$!
wait "$vite"
