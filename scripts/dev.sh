#!/usr/bin/env bash
# Dev runner: starts the Go API and the Vite SPA together.
#
#   - Frontend live-reload: Vite HMR (built in)
#   - Backend live-reload: `air` if installed, else plain `go run` (no reload)
#
# Ctrl-C stops both.

set -euo pipefail

# Always run from the repo root regardless of where the script is invoked from.
cd "$(dirname "$0")/.."

PORT="${PORT:-8080}"
DATA_DIR="${DATA_DIR:-./data}"

# --- backend runner -------------------------------------------------------
if command -v air >/dev/null 2>&1; then
  BACKEND_NAME="air (live reload)"
  BACKEND_CMD=(air)
else
  BACKEND_NAME="go run (no live reload)"
  BACKEND_CMD=(go run ./cmd/kscope --port "$PORT" --data-dir "$DATA_DIR")
  cat <<EOF
note: 'air' is not installed — the backend will not auto-reload on .go changes.
      Install for live reload:
          go install github.com/air-verse/air@latest

EOF
fi

# --- frontend deps on first run ------------------------------------------
if [ ! -d web/node_modules ]; then
  echo "Installing frontend deps..."
  npm --prefix web install
fi

# --- launch both, propagate Ctrl-C to children ---------------------------
pids=()
cleanup() {
  echo
  echo "Stopping..."
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait "${pids[@]}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Starting backend: $BACKEND_NAME  (port=$PORT, data-dir=$DATA_DIR)"
"${BACKEND_CMD[@]}" &
pids+=("$!")

echo "Starting frontend: vite (port=5173, proxying /api → :$PORT)"
npm --prefix web run dev &
pids+=("$!")

# Exit when either child exits, then cleanup trap kills the other.
wait -n
exit $?
