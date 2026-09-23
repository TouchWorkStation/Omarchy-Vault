#!/usr/bin/env bash
# Omarchy Vault installer.
#
# Every action is printed before it runs. Nothing here touches your drives:
# no formatting, partitioning, mounting or fstab edits.
#
#   ./scripts/install.sh              install for the current user (asks)
#   ./scripts/install.sh --express    everything in one go: packages, build,
#                                     /srv/vault, firewall rule for your home
#                                     network, free shortcuts, then setup
#   ./scripts/install.sh --with-files also build the Files browser (SFTPGo;
#                                     a few minutes, ~60 MB RAM while on)
#   ./scripts/install.sh --dry-run    show what would happen, change nothing
#   ./scripts/install.sh --yes        answer "yes" to optional prompts
set -euo pipefail

DRY_RUN=0
ASSUME_YES=0
NO_BUILD=0
EXPRESS=0
WITH_FILES=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --express) EXPRESS=1; ASSUME_YES=1 ;;
    --with-files) WITH_FILES=1 ;;
    --no-build) NO_BUILD=1 ;;
    -h|--help) sed -n '2,16p' "$0"; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.."
REPO="$(pwd)"
BIN_DIR="$HOME/.local/bin"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/omarchy-vault"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/omarchy-vault"

say()  { printf '\033[1m==>\033[0m %s\n' "$*"; }
note() { printf '    %s\n' "$*"; }
warn() { printf '\033[33m !\033[0m  %s\n' "$*"; }
run()  {
  printf '    + %s\n' "$*"
  if [[ $DRY_RUN -eq 0 ]]; then "$@"; fi
}
ask() {
  [[ $ASSUME_YES -eq 1 ]] && return 0
  [[ $DRY_RUN -eq 1 ]] && { note "(dry run: would ask) $1"; return 1; }
  read -r -p "    $1 [y/N] " reply
  [[ "$reply" =~ ^[Yy]$ ]]
}

[[ $DRY_RUN -eq 1 ]] && say "DRY RUN — nothing will be changed"

# 1. Never run as root: Vault runs as your user.
if [[ $EUID -eq 0 ]]; then
  echo "Run the installer as your normal user, not root. It will ask for sudo only to install packages." >&2
  exit 1
fi

# 2. Detect Omarchy / Arch.
say "Checking system"
if [[ -r /etc/os-release ]]; then
  . /etc/os-release
  if [[ "${ID:-}" == "arch" || " ${ID_LIKE:-} " == *" arch "* ]]; then
    note "Arch-based system: ${PRETTY_NAME:-$ID}"
  else
    warn "This is not Arch/Omarchy (${PRETTY_NAME:-unknown}). Vault may work, but package installs are skipped."
  fi
fi
if [[ -d "$HOME/.local/share/omarchy" ]]; then
  note "Omarchy detected"
else
  warn "Omarchy not detected; the desktop integration will be skipped."
fi

# 3. Dependencies.
say "Checking dependencies"
missing_build=()
for tool in go npm git gcc; do
  command -v "$tool" >/dev/null || missing_build+=("$tool")
done
for tool in lsblk findmnt; do
  command -v "$tool" >/dev/null || { echo "Required tool '$tool' is missing (package util-linux)." >&2; exit 1; }
done
note "lsblk, findmnt: ok"

if ! command -v smartctl >/dev/null; then
  warn "smartctl not found: drive health will show as Unknown."
  if command -v pacman >/dev/null && ask "Install smartmontools with pacman now?"; then
    run sudo pacman -S --needed smartmontools
  fi
else
  note "smartctl: ok"
fi

# 4. Build.
if [[ $NO_BUILD -eq 0 ]]; then
  if [[ ${#missing_build[@]} -gt 0 ]]; then
    warn "Building needs: ${missing_build[*]}"
    if command -v pacman >/dev/null && ask "Install them with: sudo pacman -S --needed go npm git base-devel ?"; then
      run sudo pacman -S --needed go npm git base-devel
    else
      echo "Install them (sudo pacman -S --needed go npm git base-devel) or pass --no-build with prebuilt bin/." >&2
      exit 1
    fi
  fi
  say "Building Vault"
  run make -C "$REPO" all
fi
[[ $DRY_RUN -eq 1 || -x "$REPO/bin/vaultd" ]] || { echo "bin/vaultd missing; run make all" >&2; exit 1; }

# 4b. File service (SFTPGo), built from a pinned, verified source tag into
#     your home folder. Needed for Files and user accounts' file access.
say "File service (Files)"
if [[ -x "$DATA_DIR/sftpgo/bin/sftpgo" ]]; then
  note "SFTPGo already installed in $DATA_DIR/sftpgo"
elif command -v sftpgo >/dev/null && [[ -d /usr/share/sftpgo/templates ]]; then
  note "Using the system SFTPGo ($(command -v sftpgo))"
elif [[ $WITH_FILES -eq 1 ]] || { [[ $EXPRESS -eq 0 ]] && ask "Build the file service (SFTPGo v2.7.6) now? It takes a few minutes."; }; then
  run "$REPO/scripts/build-sftpgo.sh" "$DATA_DIR/sftpgo"
else
  note "Skipped. Files stays off until you run: ./scripts/build-sftpgo.sh"
fi

# 5. Config directory (private).
say "Creating config directory"
run mkdir -p "$CONFIG_DIR"
run chmod 700 "$CONFIG_DIR"
note "Settings will live in $CONFIG_DIR/config.json (created during storage setup)."

# 6. /srv/vault shortcut. It is a symlink to a user-owned link that Vault
#    switches when you choose storage, so Vault never needs root afterwards.
say "Vault location /srv/vault"
LINK_TARGET="$DATA_DIR/current"
if [[ -L /srv/vault && "$(readlink /srv/vault)" == "$LINK_TARGET" ]]; then
  note "/srv/vault already points to Vault"
elif [[ -e /srv/vault || -L /srv/vault ]]; then
  warn "/srv/vault already exists and is not Vault's shortcut; leaving it alone."
  note "Your Vault still works; it just won't be reachable at /srv/vault."
elif ask "Create /srv/vault -> $LINK_TARGET (needs sudo once)?"; then
  run sudo ln -sn -- "$LINK_TARGET" /srv/vault
else
  note "Skipped. Create it later with: vaultctl link"
fi

# 7. Binaries.
say "Installing binaries to $BIN_DIR"
run install -d "$BIN_DIR"
run install -m 0755 "$REPO/bin/vaultd" "$BIN_DIR/vaultd"
run install -m 0755 "$REPO/bin/vaultctl" "$BIN_DIR/vaultctl"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR is not on your PATH." ;;
esac

# 8. systemd user service.
say "Installing user service"
run install -d "$UNIT_DIR"
run install -m 0644 "$REPO/systemd/omarchy-vault.service" "$UNIT_DIR/omarchy-vault.service"
run systemctl --user daemon-reload
# Vault runs only when you turn it on. The unit has no [Install] section,
# so it cannot start at login; earlier installs that enabled it are undone.
if systemctl --user is-enabled omarchy-vault.service >/dev/null 2>&1; then
  note "Removing start-at-login from an earlier install"
  run systemctl --user disable omarchy-vault.service
fi
if [[ -e "/var/lib/systemd/linger/$USER" ]]; then
  note "Note: 'linger' is on for $USER (from an earlier install or another app)."
  note "Vault does not need it. To turn it off: sudo loginctl disable-linger $USER"
fi
if systemctl --user is-active omarchy-vault.service >/dev/null 2>&1; then
  note "Vault is on; restarting it with the new version"
  run systemctl --user restart omarchy-vault.service
elif ask "Turn Vault on now? (it stays off until you turn it on)"; then
  run systemctl --user start omarchy-vault.service
else
  note "Turn it on whenever you need it: vaultctl on  (or open it with vaultctl open)"
fi

# 9. Omarchy plugin files (not wired into the panel until it is ready).
say "Installing Omarchy integration files"
run install -d "$DATA_DIR/plugin/qml"
run install -m 0644 "$REPO/plugin/manifest.json" "$DATA_DIR/plugin/manifest.json"
for f in "$REPO"/plugin/qml/*.qml; do
  run install -m 0644 "$f" "$DATA_DIR/plugin/qml/$(basename "$f")"
done

# 10. Firewall: phones reach Vault on port 8790 (only while a QR code is
# showing). Allow it from your home network only, never from anywhere.
say "Firewall (phone transfers, port 8790)"
# The home network is the private subnet on the interface of the default
# route (so Docker or VPN interfaces are never picked).
lan_subnet() {
  awk '
    $1 == "default" { for (i = 1; i < NF; i++) if ($i == "dev") dev = $(i + 1) }
    $0 ~ /proto kernel/ { for (i = 1; i < NF; i++) if ($i == "dev") net[$(i + 1)] = $1 }
    END {
      s = net[dev]
      if (s ~ /^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.)/) print s
    }'
}
SUBNET="$(ip -4 route 2>/dev/null | lan_subnet || true)"
if ! command -v ufw >/dev/null; then
  note "ufw not installed; nothing to do."
elif [[ -z "$SUBNET" ]]; then
  warn "Not connected to a home network right now; skipped."
  note "Later: sudo ufw allow from <your network, e.g. 192.168.1.0/24> to any port 8790 proto tcp"
elif ask "Allow phones on $SUBNET to reach Vault's transfer port 8790 (needs sudo)?"; then
  run sudo ufw allow from "$SUBNET" to any port 8790 proto tcp comment "Omarchy Vault phone transfers"
  [[ $DRY_RUN -eq 1 ]] || { install -d "$DATA_DIR"; echo "$SUBNET" > "$DATA_DIR/ufw-subnet"; }
else
  note "Skipped. Later: sudo ufw allow from $SUBNET to any port 8790 proto tcp"
fi

# 11. Shortcuts. Only free ones are ever added; an existing binding is
# never replaced (see docs/shortcuts.md).
if [[ $EXPRESS -eq 1 ]]; then
  say "Adding keyboard shortcuts (free ones only)"
  run "$BIN_DIR/vaultctl" shortcuts install --yes || warn "Shortcuts not added; run: vaultctl shortcuts"
else
  say "Checking shortcuts (nothing is changed)"
  if [[ $DRY_RUN -eq 0 ]]; then
    "$BIN_DIR/vaultctl" shortcuts || true
  else
    note "+ vaultctl shortcuts"
  fi
fi

say "Done"
cat <<MSG

    Turn on / off:     vaultctl on   |   vaultctl off
    Set up storage:    vaultctl setup          (turns Vault on, opens the setup screens)
    Open Vault:        vaultctl open           (turns Vault on, http://127.0.0.1:8788)
    Phone → Vault:     vaultctl upload         (QR code; phone on the same Wi-Fi)
    Add shortcuts:     vaultctl shortcuts install   (Super+Alt+V/U/D)

    Vault never runs by itself: nothing starts at login or boot.
    Check everything:  vaultctl doctor
    See your drives:   vaultctl disks

    Vault never formats, partitions or erases drives, and never overwrites
    an existing keyboard shortcut.
MSG

# 12. Express: go straight to the setup screen.
if [[ $EXPRESS -eq 1 && $DRY_RUN -eq 0 ]]; then
  say "Opening the setup screen"
  note "Pick your drive, create your account, done."
  note "Drive not listed? Click it in the Files app sidebar to mount it, then press Refresh."
  "$BIN_DIR/vaultctl" setup || warn "Could not open setup; run: vaultctl setup"
fi
