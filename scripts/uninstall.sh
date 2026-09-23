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

# Vault's own shortcut file and its one source line (before vaultctl goes).
HYPR_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/hypr"
if [[ ( -f "$HYPR_DIR/omarchy-vault.conf" || -f "$HYPR_DIR/omarchy_vault.lua" ) && -x "$HOME/.local/bin/vaultctl" ]]; then
  echo "==> Removing Vault's keyboard shortcuts (your others are untouched)"
  run "$HOME/.local/bin/vaultctl" shortcuts remove --yes || true
fi

# The firewall rule the express installer added (home network only).
if [[ -f "$DATA_DIR/ufw-subnet" ]] && command -v ufw >/dev/null; then
  SUBNET="$(cat "$DATA_DIR/ufw-subnet")"
  echo "==> Removing the firewall rule for $SUBNET (needs sudo)"
  run sudo ufw delete allow from "$SUBNET" to any port 8790 proto tcp || true
  [[ $DRY_RUN -eq 1 ]] || rm -f "$DATA_DIR/ufw-subnet"
fi

echo "==> Removing programs and service"
for f in "$HOME/.local/bin/vaultd" "$HOME/.local/bin/vaultctl" "$UNIT"; do
  [[ -e "$f" ]] && run rm -f "$f"
done
[[ -d "$DATA_DIR/plugin" ]] && run rm -rf "$DATA_DIR/plugin"
# The file service program (its database with your users stays).
for d in bin templates static; do
  [[ -d "$DATA_DIR/sftpgo/$d" ]] && run rm -rf "$DATA_DIR/sftpgo/$d"
done
[[ -f "$DATA_DIR/sftpgo/VERSION" ]] && run rm -f "$DATA_DIR/sftpgo/VERSION"
run systemctl --user daemon-reload || true

# Remove /srv/vault only if it is Vault's own shortcut.
if [[ -L /srv/vault && "$(readlink /srv/vault)" == "$DATA_DIR/current" ]]; then
  echo "==> Removing the /srv/vault shortcut (needs sudo)"
  run sudo rm -- /srv/vault
fi
[[ -L "$DATA_DIR/current" ]] && run rm -- "$DATA_DIR/current"

echo
echo "Kept: ${XDG_CONFIG_HOME:-$HOME/.config}/omarchy-vault/ (settings) and every file on your Vault drive."
echo "Remove settings yourself if you no longer want them."
