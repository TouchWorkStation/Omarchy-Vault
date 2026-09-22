#!/usr/bin/env bash
# Run vaultd (Go) and the Vite dev server together.
#   ./scripts/dev.sh          real drives on this machine (read-only)
#   ./scripts/dev.sh --demo   sample drives and shortcuts
set -euo pipefail
cd "$(dirname "$0")/.."

args=()
[[ "${1:-}" == "--demo" ]] && args+=(--demo)

if [[ ! -d web/node_modules ]]; then
  echo "==> Installing web dependencies"
  (cd web && npm ci)
fi

echo "==> Starting vaultd on http://127.0.0.1:8788 ${args[*]:-}"
go run ./cmd/vaultd "${args[@]}" &
VAULTD=$!
trap 'kill $VAULTD 2>/dev/null || true' EXIT INT TERM

echo "==> Starting UI on http://127.0.0.1:5173 (proxies /api to vaultd)"
cd web && npm run dev
