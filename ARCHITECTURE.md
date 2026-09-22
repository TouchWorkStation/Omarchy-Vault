# Architecture

Omarchy Vault is one small Go daemon (`vaultd`), a CLI (`vaultctl`), a React dashboard embedded in the daemon, and an optional Omarchy desktop plugin. It orchestrates proven tools rather than reimplementing them.

## Components

```
┌─────────────────────────────────────────────────────────────────────┐
│ Clients                                                             │
│  Browser (desktop / phone)   vaultctl (CLI)   Omarchy plugin (QML)  │
│  Beam (future, via /api/v1/beam/*)                                  │
└───────────────┬─────────────────────────────────────────────────────┘
                │ HTTP, 127.0.0.1:8788 (loopback only by default)
┌───────────────▼─────────────────────────────────────────────────────┐
│ vaultd  (runs as the user, systemd --user)                          │
│                                                                     │
│  internal/api        routing, security headers, host guard,         │
│                      CSRF origin checks, rate limit, SPA            │
│  internal/disks      lsblk + findmnt → inventory, system disk       │
│  internal/health     smartctl JSON → Healthy/Warning/Critical       │
│  internal/storage    adoption, live state, /srv/vault link          │
│  internal/auth       local token, login codes, sessions             │
│  internal/services   which tools/units exist (read-only)            │
│  internal/shortcuts  Hyprland binding conflict analysis             │
│  internal/config     ~/.config/omarchy-vault/config.json            │
│  internal/sysexec    allowlisted, shell-free command runner         │
│  web (embed)         built React UI                                 │
└───────┬─────────────────────────────┬───────────────────────────────┘
        │ read-only exec (no shell)   │ later milestones
        ▼                             ▼
  lsblk · findmnt · smartctl     SFTPGo (files, users, WebDAV, SFTP)   M3
  hyprctl · systemctl is-active  SQLite (tokens, shares, activity)     M4
                                 vault-helper (privileged, allowlist)  M7
                                 mergerfs · cloudflared · samba        M6/M7
```

## Principles

1. **Orchestrate, don't reinvent.** Files and users are SFTPGo's job, pooling is mergerfs's, tunnels are cloudflared's. Vault owns the experience and the safety rules.
2. **Read before write.** Milestone 1 is entirely read-only. Every later write goes through a narrow, named operation (for example `mount_pool()`), never a generic command.
3. **Least privilege.** `vaultd` runs as the desktop user. Root is needed only for a few operations (mounting a pool, SMART on some drives, installing services). Those go to a separate privileged helper with a fixed allowlist of operations and validated arguments (see SECURITY.md).
4. **Local first.** Binding to loopback is the default. Remote access is a tunnel the user turns on.
5. **Lightweight.** One static binary, embedded UI, no background filesystem scanning. Idle RSS is about 10 MB today; the target is under 100 MB.

## Data flow: drive discovery (Milestone 1)

1. `GET /api/disks` asks `disks.Scanner` for an inventory.
2. The scanner returns a cached result if it is younger than 30 s (a forced refresh is still limited to one scan per 5 s).
3. Otherwise it runs `lsblk -J -b -o …` and `findmnt -J -o TARGET,SOURCE,FSTYPE` through `sysexec`.
4. `disks.Build` (a pure function, fully unit-tested) walks each drive's device tree. Any drive whose tree backs `/`, `/boot`, `/boot/*`, `/efi`, `/usr`, `/var`, `/home` or swap is **SYSTEM · PROTECTED**. lsblk mountpoints and the kernel mount table are two independent signals; either one is enough.
5. If no system disk can be identified, nothing is adoptable (fail safe).
6. Volumes are classified: mounted and supported (adoptable), unmounted, container (LUKS/LVM/RAID), unsupported, swap. Mounts under reserved paths (`/var`, `/usr`, `/etc`, …) are never adoptable.
7. SMART is queried with `smartctl -j -n standby -i -H -A /dev/X` (read-only, does not wake sleeping disks), cached for 30 minutes per drive.

## Storage layout (Milestone 2)

```
/srv/vault ──► ~/.local/share/omarchy-vault/current ──► /mnt/wdred/Vault
 (root-owned symlink,        (user-owned symlink,          (folder on the
  created once, asks)         switched atomically by vaultd)  adopted drive)

/mnt/wdred/Vault/
  Photos/  Documents/  Backups/  Projects/  Phone Uploads/  Shared/   (created only if missing)

~/.config/omarchy-vault/
  config.json               0600, no secrets; sources[] = {path, folder, uuid, volume, label}
  secrets/                  0700
    local-token             0600, CLI write authorization
  vault.db                  SQLite from Milestone 4: tokens (hashed), shares, activity
```

Default folders are created only if missing; existing files and folders are never overwritten. In Milestone 7, combining drives mounts a mergerfs pool (FUSE, as the user) and points `current` at it; `/srv/vault` does not change.

## Data flow: choosing storage (Milestone 2)

1. The setup screen or `vaultctl storage use` sends `POST /api/pool {volume: "sda1", folder: "Vault"}`. Browsers use the session cookie plus `X-Vault-Request`; the CLI uses `X-Vault-Token`.
2. vaultd forces a fresh drive scan and runs `storage.Adopt`. It refuses the system disk, unknown system disk, unadoptable volumes, unmounted mount points, symlinks and nested mounts. It creates only missing folders, through `os.Root`.
3. `config.Save` writes atomically. vaultd then points `current` at the new folder.
4. `GET /api/storage` recomputes state every time: the UUID must still be present, the mount point must be a kernel mount, and the folder must be on that filesystem. The result is ready / drive_missing / drive_moved / problem.

## API

`/api/*` and `/api/v1/*` serve the same endpoints. Planned endpoints are registered already and answer `501` with the milestone that delivers them, so clients can be written against the final shape. See [docs/api.md](docs/api.md).

## Transfers (Milestones 4–5)

```
Super+Shift+U → vaultctl upload → POST /api/upload-session
   → 32-byte random token, stored as SHA-256 hash, scope = "upload into Phone Uploads", expires 10 min
   → QR: https://<host>/u/<token>   (no paths in the URL)
Phone → GET /u/<token> (mobile page) → POST /u/<token>/files (streamed to disk, size-limited)
```

Download sessions mirror this with scope = one resource, `max_downloads` (default 1). Folders are streamed as a zip built on the fly, with paths relative to the shared folder. Tokens are redacted from logs.

## Beam integration

Beam stays independent. When it wants persistent storage it can call:

- `POST /api/v1/beam/upload-session`
- `POST /api/v1/beam/download-session`
- `POST /api/v1/beam/share`

These return the same token/QR payloads as Vault's own flows. Authentication for local API clients (a per-client key stored 0600) arrives with Milestone 4.

## Repository layout

```
cmd/vaultd, cmd/vaultctl     binaries
internal/…                   packages listed above
web/                         React + Vite + TypeScript UI (embedded via web/embed.go)
plugin/                      Omarchy integration: manifest.json + QML widget/panel
scripts/                     install.sh, uninstall.sh, dev.sh
systemd/                     omarchy-vault.service (user unit)
docs/                        development, security model, API, shortcuts
```

Packages named in the original plan that have no code yet (`auth`, `upload`, `download`, `shares`, `cloudflare`) are created in the milestone that needs them, rather than as empty stubs.
