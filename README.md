# Omarchy Vault

Turn unused storage in your Omarchy machine into a secure private cloud.

**Upload. Download. Backup. Anywhere.**

> Beam moves it.
> Vault keeps it.

Omarchy Vault keeps Omarchy as your operating system and adds the useful parts of a NAS on top: one place for your files, access from any browser, QR-code transfer to and from your phone, backups, secure share links, and optional remote access through your own domain.

It is **not** a replacement for TrueNAS, Unraid or ZimaOS. There is no separate NAS OS to install, and no RAID to configure. Vault uses the drives you already have, as they are, and hides the Linux plumbing (mounts, mergerfs, SFTPGo, Cloudflare Tunnel, systemd, permissions) behind simple actions.

| You do | Vault handles |
|---|---|
| Combine these drives into one Vault | mergerfs pool, non-destructive |
| Create user | SFTPGo accounts, folders, permissions |
| Connect your domain | cloudflared tunnel, TLS, service |
| Upload to Vault | a short-lived, upload-only link and a QR code |

![Vault dashboard](docs/screenshots/dashboard.png)

## Status

**Milestones 1–3 of 7 are complete.** Vault protects your system disk, turns a mounted drive into your Vault, and gives everyone in the house their own account and a file browser. See [ROADMAP.md](ROADMAP.md).

| Works now | Coming |
|---|---|
| First-run setup: choose a drive, create your admin account | **Upload to Vault**: Super+Shift+U, QR code (M4) |
| **Files** in the browser: browse, upload, download, folders (M3) | **Download from Vault**: Super+Shift+D, QR code (M5) |
| **Users**: Admin / Family / Guest, per-folder read & write or read only (M3) | Share links (M5) |
| Sign-in with lockout, optional two-factor (TOTP) (M3) | Remote access through Cloudflare Tunnel (M6) |
| Your Vault at `/srv/vault`; unplugged drives never fill the system disk (M2) | Combining several drives, LAN sharing (M7) |
| Drive discovery, SYSTEM · PROTECTED detection, SMART health (M1) | Backups (M8) |

**New here? Follow the [setup guide](docs/setup-guide.md)**: preparing a drive, installing, first-run, Files and users, step by step.

## Features (planned for v0.1)

- **Storage**: use one drive, or combine several into one Vault. Existing filesystems only; nothing is formatted.
- **Files**: browse, upload and download from any browser. SFTP/WebDAV for power users (off by default).
- **Upload to Vault** (`Super + Shift + U`): scan a QR code with your phone, pick photos, videos or files. They land in `Phone Uploads`.
- **Download from Vault** (`Super + Shift + D`): pick a file or folder, scan the QR code, it downloads to your phone.
- **Share links**: read-only, expiring, optionally password-protected, revocable.
- **Users**: Admin, Family, Guest; read/write or read-only per folder.
- **Remote access**: your own domain through Cloudflare Tunnel. Vault is never exposed directly.
- **Backups**: copy computer folders into `Backups/<hostname>/` with rsync.
- **Drive health**: SMART status, temperature, power-on hours, reallocated sectors. Errors are never hidden.

## Quick start

Requirements: Omarchy or Arch Linux with `git go npm base-devel` (the installer offers to install them). Optional: `smartmontools` for drive health. You also need a second drive that is mounted; see [preparing your drive](docs/setup-guide.md#2-prepare-your-drive).

```sh
git clone https://github.com/TouchWorkStation/Omarchy-Vault.git ~/Omarchy-Vault
cd ~/Omarchy-Vault

./scripts/install.sh --dry-run   # see exactly what will happen
./scripts/install.sh             # build Vault + the file service, install for your user
vaultctl setup                   # turns Vault on, then choose your drive and create your account
```

Vault **only runs when you turn it on**. Nothing starts at login or boot.

```sh
vaultctl on                      # turn on
vaultctl off                     # turn off (stops Files too)
vaultctl open                    # open the dashboard, signed in (turns Vault on if needed)
vaultctl users                   # list accounts; `vaultctl users add ann --role family`
vaultctl storage                 # your Vault drive and drives you could use
vaultctl doctor                  # check everything
```

Try it without installing, on any Linux machine:

```sh
make all
./bin/vaultd --demo    # sample drives in a throwaway sandbox, clearly labelled
```

Developing? See [docs/development.md](docs/development.md).

## Architecture

```
 browser / phone ──► vaultd (Go, 127.0.0.1:8788) ──► read-only system tools
                       │  REST API + embedded React UI    lsblk · findmnt · smartctl · hyprctl
                       │
 vaultctl (CLI) ───────┤  SFTPGo (Files, per-user folders) · later: mergerfs (pooling)
 Omarchy plugin (QML) ─┘         cloudflared (remote) · privileged helper (narrow, allowlisted)
```

One Go binary serves the API and the dashboard, and supervises SFTPGo (the file engine) as a child process on 127.0.0.1:8789, reachable only through Vault's sign-in at `/files/`. SQLite (from Milestone 4) holds tokens, shares and activity, never files. Details: [ARCHITECTURE.md](ARCHITECTURE.md).

## Safety model

- **Vault never formats, partitions, erases or rewrites partition tables.** It only uses filesystems that are already mounted.
- **The system disk is always protected.** Any drive backing `/`, `/boot`, EFI, `/usr`, `/var`, `/home` or swap is marked SYSTEM · PROTECTED. If Vault cannot identify the system disk, it offers no drive at all.
- **Unplugged drives can't fill your system disk.** Vault checks the drive's UUID and the kernel mount table before every use. If the drive is gone, the Vault shows as offline instead of writing into an empty folder.
- **Adopting a drive only adds folders.** Existing files are never moved, renamed or deleted. "Stop using this drive" only forgets it.
- **Accounts done carefully.** Passwords are argon2id hashes, sign-in locks out after repeated failures, two-factor is optional, and disabling someone signs them out immediately. Family and guests only see the folders you give them.
- **Local by default.** The service binds to `127.0.0.1` and rejects unknown `Host` headers (DNS-rebinding protection). SFTP and WebDAV are off. Remote access is opt-in and goes through a tunnel.
- **No shell from the web.** There is no command-execution endpoint. Vault runs a short allowlist of read-only tools, without a shell.
- **Shortcuts are never overwritten.** Conflicts are reported with options: choose another, copy the binding, or skip.

Full threat model: [SECURITY.md](SECURITY.md).

## Beam and Vault

Beam and Vault are separate projects. Vault does not depend on Beam and does not modify it. Vault reserves `POST /api/v1/beam/*` endpoints so Beam can later ask Vault for upload sessions, download sessions and shares ([docs/api.md](docs/api.md)).

## Screenshots

| Setup: choose a drive | Setup: create your Vault |
|---|---|
| ![Choose a drive](docs/screenshots/setup-storage.png) | ![Create your Vault](docs/screenshots/setup-vault.png) |

| Users | Files (a family member's view) |
|---|---|
| ![Users](docs/screenshots/users.png) | ![Files](docs/screenshots/files-family.png) |

| Storage | Phone |
|---|---|
| ![Storage page](docs/screenshots/storage.png) | ![Ready on a phone](docs/screenshots/setup-ready-phone.png) |

Screenshots use `--demo` data.

## Roadmap

1. ✅ Foundation: dashboard, read-only drive discovery, shortcut planning
2. ✅ Single-drive Vault, config, `/srv/vault`, system disk protection
3. ✅ SFTPGo files and users
4. Upload to Vault (Super+Shift+U), QR, mobile upload page
5. Download from Vault (Super+Shift+D), file picker, mobile download page
6. Remote access through Cloudflare Tunnel
7. Combining drives with mergerfs, SMART monitoring, optional LAN sharing

Details in [ROADMAP.md](ROADMAP.md).

## License

MIT. See [LICENSE](LICENSE).
