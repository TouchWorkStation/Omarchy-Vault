#!/usr/bin/env bash
# Build the file service (SFTPGo) that Vault uses for Files.
#
# Installs a pinned, verified release into your home folder. No root needed.
#   ./scripts/build-sftpgo.sh [destination]
# Default destination: ~/.local/share/omarchy-vault/sftpgo
#
# The source is cloned from the official repository at a fixed tag, and the
# build stops unless the tag resolves to the exact commit below.
set -euo pipefail

VERSION="v2.7.6"
COMMIT="62ae9ba3957e9ed52b44a4f885e805e2d7b35972"
REPO="https://github.com/drakkan/sftpgo"
DEST="${1:-${XDG_DATA_HOME:-$HOME/.local/share}/omarchy-vault/sftpgo}"

for tool in git go gcc; do
  if ! command -v "$tool" >/dev/null; then
    echo "build-sftpgo: '$tool' is required (sudo pacman -S --needed git go base-devel)" >&2
    exit 1
  fi
done

if [[ -x "$DEST/bin/sftpgo" ]] && "$DEST/bin/sftpgo" --version 2>/dev/null | grep -q "${VERSION#v}"; then
  echo "==> SFTPGo $VERSION is already installed in $DEST"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> Downloading SFTPGo $VERSION source"
git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$VERSION" "$REPO" "$tmp/src"
got="$(git -C "$tmp/src" rev-parse HEAD)"
if [[ "$got" != "$COMMIT" ]]; then
  echo "build-sftpgo: $VERSION resolved to $got, expected $COMMIT. Refusing to build." >&2
  exit 1
fi

echo "==> Building SFTPGo (this takes a few minutes the first time)"
(cd "$tmp/src" && CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o "$tmp/sftpgo" .)

echo "==> Installing into $DEST"
install -d -m 0755 "$DEST/bin"
install -m 0755 "$tmp/sftpgo" "$DEST/bin/sftpgo"
rm -rf "$DEST/templates" "$DEST/static"
cp -r "$tmp/src/templates" "$tmp/src/static" "$DEST/"
printf '%s %s\n' "$VERSION" "$COMMIT" > "$DEST/VERSION"
"$DEST/bin/sftpgo" --version | head -1
