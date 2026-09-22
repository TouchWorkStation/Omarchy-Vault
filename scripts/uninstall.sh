#!/usr/bin/env bash
# Remove Omarchy Vault's programs and service. Your settings and every file
# in your Vault are left untouched.
#   ./scripts/uninstall.sh [--dry-run]
set -euo pipefail

DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

run() { printf '    + %s\n' "$*"; [[ $DRY_RUN -eq 1 ]] || "$@"; }

UNIT="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/omarchy-vault.service"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/omarchy-vault"

echo "==> Stopping Vault"
if systemctl --user list-unit-files omarchy-vault.service >/dev/null 2>&1; then
  run systemctl --user disable --now omarchy-vault.service || true
fi

echo "==> Removing programs and service"
for f in "$HOME/.local/bin/vaultd" "$HOME/.local/bin/vaultctl" "$UNIT"; do
  [[ -e "$f" ]] && run rm -f "$f"
done
[[ -d "$DATA_DIR/plugin" ]] && run rm -rf "$DATA_DIR/plugin"
run systemctl --user daemon-reload || true

echo
echo "Kept: ${XDG_CONFIG_HOME:-$HOME/.config}/omarchy-vault/ (settings) and all Vault storage."
echo "Remove settings yourself if you no longer want them."
