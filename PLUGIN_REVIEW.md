# Notes for plugin reviewers

Thanks for reviewing Omarchy Vault. This page covers what the plugin does, what it touches and how to check it quickly.

**In one sentence:** a bar widget (`touchworkstation.vault`) that opens a small local app for moving files between the computer and phones on the same Wi-Fi by QR code. There is no cloud, no telemetry, and nothing runs in the background.

## What loads in the shell

| | |
|---|---|
| Manifest | [`manifest.json`](manifest.json): `schemaVersion` 1, kind `bar-widget`, entry point `plugin/VaultWidget.qml`, `defaultSection: right` |
| QML | [`plugin/VaultWidget.qml`](plugin/VaultWidget.qml), 71 lines, the only file the shell loads |
| Imports | `QtQuick`, `Quickshell.Io` (`Process`), `qs.Commons` (`Style`), `qs.Ui` (`BarWidget`, `BarIconButton`) |
| At load | Runs one command: `sh -c 'test -x "$HOME/.local/bin/vaultctl"'`, to decide whether Vault is installed. It runs again after each click. |
| Timers, polling, sockets | None |
| On click | `bar.run(...)` with one fixed command per button (below). Nothing from outside the widget is put into a command. |

| Action | Command |
|---|---|
| Click | `$HOME/.local/bin/vaultctl open` |
| Right-click | `$HOME/.local/bin/vaultctl upload` |
| Middle-click | `$HOME/.local/bin/vaultctl download` |
| Any click, before Vault is installed | `xdg-terminal-exec bash -c '<plugin dir>/scripts/install.sh --express; …'` in a visible terminal |

`omarchy plugin add` only clones and validates, as usual. Vault itself is installed only when the user clicks the dimmed icon, and every step is printed in the terminal.

## What the installer does

[`scripts/install.sh`](scripts/install.sh) (`--express` answers "yes" to its questions; `--dry-run` prints everything and changes nothing):

| Step | Where | sudo |
|---|---|---|
| Install build tools if missing: `pacman -S --needed go npm git base-devel`, optionally `smartmontools` | system packages | yes |
| Build Vault from a `git archive` copy of the plugin checkout | `~/.cache/omarchy-vault/build` | no |
| Install `vaultd` and `vaultctl` | `~/.local/bin` | no |
| Install a **user** systemd unit with **no `[Install]` section**, so it never starts at login | `~/.config/systemd/user/omarchy-vault.service` | no |
| Create the `/srv/vault` symlink, pointing at a user-owned link | `/srv/vault` | yes, once |
| Allow TCP 8790 from the LAN subnet only (the subnet of the default route's interface; never `0.0.0.0/0`, skipped when ufw is absent) | ufw | yes |
| Add keyboard shortcuts (below) | `~/.config/hypr/omarchy_vault.lua` plus one line in `hyprland.lua` | no |
| Open the setup screen | browser | no |

Things it never does: format, partition or mount drives; edit `/etc/fstab`; run as root; enable lingering; touch other plugins or Omarchy's files; run anything at `plugin add` or `plugin update` time.

**The plugin checkout stays pristine.** Building inside `~/.config/omarchy/plugins/` would create `node_modules` symlinks (which the shell refuses) and untracked files (which would get in the way of `omarchy plugin update`). So the installer builds a `git archive` copy in `~/.cache` instead. We checked that the checkout has zero changes and zero symlinks after a build, and still passes `omarchy plugin validate`.

**Network use during install:** `npm ci` (pinned by `web/package-lock.json`) and Go modules (pinned by `go.sum`). The optional Files browser (`--with-files`, not part of `--express`) builds SFTPGo v2.7.6 from source and checks commit `62ae9ba3957e…` before building.

## Keyboard shortcuts

Defaults are Super + Alt + V / U / D. The shortcut code is [`internal/shortcuts/`](internal/shortcuts/).

- It reads the running Hyprland's bindings (`hyprctl binds -j`) and the user's config, and **never replaces an existing binding**. A taken key is skipped, or given a checked-free alternative only if the user asks.
- **Lua config (current Omarchy):** writes `~/.config/hypr/omarchy_vault.lua` (using `o.bind` when present, else `hl.bind`, each wrapped in `pcall`). It appends two lines to `hyprland.lua`:
  ```lua
  -- Omarchy Vault shortcuts (remove with: vaultctl shortcuts remove)
  pcall(dofile, (os.getenv("HOME") or "") .. "/.config/hypr/omarchy_vault.lua")
  ```
  A missing or broken Vault file can therefore never break the user's config. It then runs `hyprctl reload`.
- **Older hyprlang config:** the equivalent `source =` line in `hyprland.conf`.
- **Removal:** `vaultctl shortcuts remove` deletes Vault's file and exactly those lines, leaving the config byte for byte as before (tested).
- It refuses to overwrite a file of the same name that it didn't write.

## What runs, and when

- Vault runs only when started, by a click, a shortcut or `vaultctl on`. It **stops itself after 15 minutes** with no use, no active link and no transfer in progress (`auto_off_minutes`).
- The dashboard listens on `127.0.0.1:8788` only, with a Host allowlist, Origin and `Sec-Fetch-Site` checks, a strict CSP, and no command-execution endpoint.
- Phones reach a **separate listener on the LAN address, port 8790, which exists only while a QR link is active**. It serves only pages for valid 256-bit tokens (stored as SHA-256 hashes).
  - Upload links are upload-only.
  - Download links are for one item.
  - Share links are read-only and last at most 24 hours.
- **Sending files copied in the file manager** (Super + Alt + D) is allowed only with the local owner token, which is a 0600 file that `vaultctl` reads. A browser session can never do it. It is also refused for:
  - symlinks
  - `~/.ssh`, `~/.gnupg`, `~/.password-store`, keyrings and Vault's own config
  - `/etc`, `/proc`, `/sys`, `/dev`, `/boot`, `/root`, and `/run` except drives the file manager mounts under `/run/media`
  - any folder that contains one of these
- Typical memory while on is about 40 to 55 MB (the optional SFTPGo adds about 60 MB). Nothing runs when off.

Full threat model: [`SECURITY.md`](SECURITY.md).

## Removal

```sh
~/.config/omarchy/plugins/touchworkstation.vault/scripts/uninstall.sh   # --dry-run to preview
omarchy plugin remove touchworkstation.vault
```

`uninstall.sh` removes:
- the shortcuts
- the programs and the user unit
- the build cache
- the ufw rule it added
- the `/srv/vault` link, only if it is Vault's own

It keeps the user's settings (`~/.config/omarchy-vault`) and every file on their drive.

## Quick ways to check

```sh
omarchy plugin validate .              # passes; no symlinks, entry point exists
./scripts/install.sh --dry-run         # every step, nothing changed
make all && ./bin/vaultd --demo        # full UI on sample drives, in a throwaway sandbox under ~/.cache; real drives and config are not touched
go test -race ./...                    # unit and integration tests (shortcut install/remove, path safety, tokens, transfers)
```

## Known limits

- Phone transfers use plain HTTP on the local network (documented). Users are told not to use them on untrusted Wi-Fi.
- "Copy, then Super + Alt + D" reads the clipboard with `wl-paste` (Wayland) and only when the shortcut is pressed. It accepts `x-special/gnome-copied-files`, `text/uri-list` or plain absolute paths.
- **Tested on the author's Omarchy Quattro machine:**
  - installation
  - the Lua shortcuts
  - uploads from and downloads to a phone
  - "copy, then Super + Alt + D", including from a drive the Files app mounted
- **Not yet confirmed:** the bar widget has been linted against the `qs.Ui` API, but not yet loaded in a running shell by the author. Please mention it if the widget misbehaves.

Contact: open an issue at https://github.com/TouchWorkStation/Omarchy-Vault/issues.
