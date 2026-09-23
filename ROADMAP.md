# Roadmap

Vault is built in milestones. Each ends in a working, tested, committed state. Nothing destructive happens in any milestone of v0.1.

## ✅ Milestone 1: Foundation

- Repository, docs (README, ARCHITECTURE, SECURITY, ROADMAP, CONTRIBUTING)
- Go backend skeleton (`vaultd`, `vaultctl`), React + Vite frontend
- `GET /api/status`, `/api/disks`, `/api/storage`, `/api/settings`, `/api/shortcuts`, `/api/services`
- Read-only drive discovery with SYSTEM · PROTECTED detection (fail-safe)
- SMART health parsing (Healthy / Warning / Critical / Unknown)
- Dashboard, Storage and Settings pages; desktop and phone layouts
- Shortcut plan and Hyprland conflict detection (no installation)
- `vaultctl doctor`, safe installer with `--dry-run`, systemd user unit
- Planned endpoints answer 501 with their milestone
- `--demo` mode for UI development

## ✅ Milestone 2: Single-drive Vault

- First-run flow: Welcome → Storage → Vault Storage → Account → Ready
- Adopt one mounted drive (a `Vault` folder on it, or the whole drive); `POST /api/pool`, `DELETE /api/pool`
- System disk protection re-verified at adoption time on a fresh scan; clients send a volume name, never a path
- Folder creation through `os.Root` (no symlink escapes), nested-mount check, never overwrites files
- Default folders created only if missing
- `config.json` written atomically (0600, 0700 directory)
- `/srv/vault` → `~/.local/share/omarchy-vault/current` → drive folder; one-time `sudo ln` via installer or `vaultctl link`, no root at runtime
- Live storage state: ready / drive missing / drive moved / problem (UUID + kernel mount table)
- Local write authorization: 0600 token for the CLI, single-use login code → HttpOnly SameSite=Strict session for the browser
- `vaultctl setup`, `storage use`, `storage forget`, `link`; `vaultctl open` signs the browser in
- Demo mode runs in a throwaway sandbox and never touches real config

## ✅ Milestone 3: Files and users

- SFTPGo v2.7.6 built from a pinned, verified commit (`scripts/build-sftpgo.sh`) into your home folder
- Vault runs SFTPGo as a supervised child: loopback only, web admin/SFTP/WebDAV/FTP off, restarts on crash, stops when storage goes offline
- Files behind Vault's sign-in at `/files/`; signing in to Vault signs you in to Files too
- Accounts: argon2id (the same hash SFTPGo accepts), sessions bound to a user and role, lockout after repeated failures, optional TOTP 2FA with replay protection
- Roles: Admin (everything), Family (chosen folders, rw/ro), Guest (chosen folders, read only); permissions without symlink/chmod rights
- Users page and `vaultctl users` (add, disable/enable, reset password, folders, remove); Account page (password, 2FA)
- First-run Account screen creates your admin; everything is behind sign-in afterwards
- Complete [setup guide](docs/setup-guide.md)

## ✅ Milestone 4: Upload to Vault

- SQLite (`vault.db`: upload links as SHA-256 hashes, received-file activity)
- `POST /api/upload-session`, 256-bit tokens, 10-minute default expiry (1–60), Stop at any time; 1000 files / 100 GB per link
- QR code generation (in-process, no network): on screen as SVG, in the terminal as text
- Phone listener on port 8790 that exists **only while a link is active**, serving only the upload page
- Mobile upload page: Select Photos, Take Photo, Select Videos, Choose Files; per-file progress; result list; Upload more
- Files stream straight to the drive, never overwrite (`photo (1).jpg`), keep 1 GB free
- Dashboard: Upload page, live QR page with countdown and received list, Recent files on Home
- `vaultctl upload [--folder] [--minutes] [--terminal]`; Super+Shift+U
- `vaultctl shortcuts install / remove`: installs only free shortcuts, after showing exactly what it writes
- Beam: `POST /api/v1/beam/upload-session` (local owner token)

## ✅ Milestone 5: Download from Vault

- File picker in the dashboard (`GET /api/browse`, only folders you can open; no symlinks, hidden or unfinished files); Super+Shift+D opens it
- `vaultctl download [<file or folder>] [--minutes N] [--downloads N] [--terminal]`
- `POST /api/download-session`: one file or folder, 10 minutes and 1 download by default (up to 60 min, 10 phones); a download counts once per phone, so resuming doesn't use it up
- Folders download as a zip streamed on the fly (no temporary copy)
- Mobile download page with no JavaScript needed to download
- Share links: `POST /api/share`, `GET /api/shares`, `DELETE /api/share/{id}`; read only; 10 min to 24 hours; one, limited or unlimited downloads; optional password (argon2id, lockout on guessing); shared folders list their files and download as .zip
- `vaultctl share <file or folder> [--expires 24h] [--downloads N] [--password]`
- Guests can download to their own phone but can't create share links
- Beam: `POST /api/v1/beam/download-session`, `POST /api/v1/beam/share`
- Deferred to Milestone 6: per-client API keys for local tools (Beam uses the local owner token, which only your user can read)

## Milestone 6: More drives, health, LAN

- Combine drives with mergerfs (non-destructive), per-drive capacity and health
- SMART checks via the privileged helper while Vault is on, with clear alerts (no background service)
- Optional LAN sharing with Samba (off by default)
- Per-client API keys for local tools such as Beam

## Not planned for v0.1

Formatting or partitioning drives, RAID, ZFS management, a custom sync engine, a native phone app.

Remote access from outside your home (Cloudflare Tunnel, VPN) and automatic backups were in the original plan and were dropped: Vault is local to your Wi-Fi and only runs for a short time when you use it.
