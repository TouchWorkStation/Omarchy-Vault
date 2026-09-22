# Security

Vault stores private files. Security and data safety come before features.

**Reporting a vulnerability:** please open a private security advisory on GitHub (Security → Report a vulnerability) rather than a public issue.

## Threat model

### Assets

1. The user's files in the Vault and on attached drives.
2. The operating system disk and its data.
3. Credentials: user passwords, TOTP secrets, SFTPGo admin credentials, Cloudflare tunnel token.
4. Transfer and share tokens.
5. The integrity of the user's desktop configuration (Hyprland bindings, systemd units).

### Adversaries

| Adversary | Example | Primary controls |
|---|---|---|
| Malicious website in the user's browser | DNS rebinding to 127.0.0.1, cross-site POST | Host allowlist, Origin / Sec-Fetch-Site checks, SameSite cookies, CSP |
| Someone on the LAN | Scanning for open services | Loopback bind by default; SMB/SFTP off by default |
| Someone on the internet | Probing `vault.example.com` | Tunnel only, auth + 2FA, rate limits, no directory listing without auth |
| Holder of a leaked QR/share link | Photo of a screen, forwarded message | Short expiry, single scope, max uses, revocation, read-only/upload-only |
| Authenticated but limited user (Guest) | Reading another user's folder | SFTPGo per-user virtual folders and permissions, path validation |
| Buggy Vault code | Wrong disk chosen, path traversal | Read-only M1, fail-safe system disk detection, no formatting ever, tests |

### Out of scope

A root-level attacker on the machine, physical theft of unencrypted drives (use LUKS), and compromise of Cloudflare itself.

## Trust boundaries

```
 Internet ──(Cloudflare Tunnel, TLS)──┐
 LAN (off by default) ────────────────┤
 Browser on this machine ─────────────┼──► vaultd (user)  ──► privileged helper (root, allowlist)
 vaultctl / plugin / Beam ────────────┘       │                     │
                                              ▼                     ▼
                                       user's files          mounts, services, SMART
```

1. **Network → vaultd.** Everything from the network is untrusted. vaultd binds to `127.0.0.1:8788`. Listening elsewhere requires `security.allow_non_loopback_listen: true` in config.
2. **vaultd → system.** vaultd runs as the desktop user and can only execute an allowlisted set of binaries (`internal/sysexec`): `lsblk`, `findmnt`, `blkid`, `smartctl`, `hyprctl`, `systemctl`. No shell is ever used. Arguments are fixed in code; the only discovered value passed as an argument (a device path for `smartctl`) must match `^/dev/[a-z0-9_-]+$`.
3. **vaultd → privileged helper** (from Milestone 2). See below.

## Privileged helper model

Some actions need root: mounting a mergerfs pool, reading SMART on SATA drives, enabling system services, creating `/srv/vault`. These will be handled by `vault-helper`, a separate small binary run by systemd as root and reachable only through a UNIX socket owned by the Vault user (mode 0600, peer credentials checked).

It exposes a fixed set of operations, each with validated, typed arguments:

| Allowed | Arguments validated against |
|---|---|
| `list_disks()` | none |
| `smart_status(device)` | device must exist in the current lsblk inventory |
| `ensure_vault_root()` | fixed path from config, never `/` or a system path |
| `mount_pool(sources)` | each source must be an adoptable mounted volume, not on the system disk |
| `unmount_pool()` | only Vault's own pool mount |
| `service_status(name)` / `enable_service(name)` | name from a fixed list: `sftpgo`, `cloudflared`, `smb` |

Forbidden, permanently: `exec(command)`, `run_shell(command)`, arbitrary paths, arbitrary unit names, formatting, partitioning, `wipefs`, `mkfs`, `dd`, `fdisk`/`parted`, editing `/etc/fstab` outside a Vault-owned, clearly marked block.

**The web UI never exposes sudo or shell execution.** There is no endpoint that accepts a command.

## Token model (Milestones 4–5)

- 32 bytes from `crypto/rand`, base64url in the URL. Only the SHA-256 hash is stored (SQLite). Lookup compares hashes in constant time.
- Every token has one scope: upload into one folder, or download one file/folder, or view one share.
- Upload tokens: upload-only, cannot list or read; default 10-minute expiry; size and count limits; revocable.
- Download tokens: one resource; default 10 minutes and 1 download; configurable.
- Share links: read-only always; expiry 10 min / 1 h / 24 h / custom; one / limited / unlimited downloads until expiry; optional password (argon2id); manual revoke.
- URLs never contain filesystem paths. Filenames from phones are sanitised (no separators, no leading dots, no control characters, length-limited) and never overwrite existing files.
- Token path segments (`/u/…`, `/d/…`, `/s/…`) are redacted in logs.

## Web security

Implemented in Milestone 1 (`internal/api/middleware.go`, tested):

- **Host allowlist**: `localhost`, `127.0.0.1`, `::1`, plus configured domains. Others get 421. Blocks DNS rebinding.
- **CSRF**: non-GET requests with a foreign `Origin`, or `Sec-Fetch-Site: cross-site`, are refused. When sessions arrive (M3), cookies are `HttpOnly`, `Secure` (over the tunnel), `SameSite=Strict`, and state-changing requests also require a CSRF token.
- **Headers**: strict CSP (`default-src 'self'`, no inline script, `frame-ancestors 'none'`), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, restrictive `Permissions-Policy`, COOP/CORP same-origin, `Cache-Control: no-store` on the API.
- **Rate limiting**: per-client token bucket on all requests; stricter limits for login and token endpoints from M3/M4. Behind the tunnel, the client IP comes from `CF-Connecting-IP` only when the request arrives from the local cloudflared.
- **Limits**: 1 MB API bodies, 64 KB headers, read/write timeouts.
- **Static files** are served from the embedded build only; there is no path from a URL to the host filesystem.

Planned: authentication (M3) with argon2id password hashes, optional TOTP 2FA, session rotation on login, lockout with backoff.

## Path safety (from Milestone 3)

- All user-supplied paths are resolved relative to a Vault root, cleaned, and rejected if they contain `..`, NUL, or absolute components.
- Final paths are checked after resolving symlinks (`openat2` with `RESOLVE_BENEATH` where available, `filepath.EvalSymlinks` + prefix check otherwise) to prevent symlink escapes.
- Downloads of folders stream an archive with relative names only.

## Remote access model (Milestone 6)

- Only Cloudflare Tunnel. Vault never opens router ports and never listens publicly by default.
- The tunnel exposes Vault's web interface only. **SMB is never exposed through the tunnel.** SFTP/WebDAV are not exposed unless the user explicitly enables them.
- Remote access requires authentication; enabling it prompts for 2FA.
- The tunnel token is stored in `~/.config/omarchy-vault/secrets/` with `chmod 600`, never logged, never returned by the API, and excluded from git by `.gitignore` patterns.

## Storage safety

- v0.1 never formats, partitions, erases filesystems, rewrites partition tables or modifies the root disk.
- Only already-mounted, supported filesystems (ext4, xfs, btrfs, f2fs; exFAT/NTFS/FAT with warnings) are adoptable.
- A drive backing `/`, `/boot`, `/boot/*`, `/efi`, `/usr`, `/var`, `/home` or swap is SYSTEM · PROTECTED. Detection uses lsblk mountpoints **and** the kernel mount table.
- If the system disk cannot be identified, **no** drive is adoptable.
- Mounts under reserved locations (`/usr`, `/var`, `/etc`, `/boot`, `/tmp`, …) are never adoptable.
- Unsupported drives (no filesystem, locked LUKS, LVM/RAID members) are shown, never changed.
- mergerfs pools are non-destructive: every drive stays individually readable.

## Shortcut installation safety

- Vault reads bindings from the running Hyprland (`hyprctl binds -j`) and from the config tree (`~/.config/hypr/hyprland.conf` and everything it sources, honoring `unbind` and submaps).
- A planned shortcut is only installed if it is free. Conflicts are reported as **Shortcut Conflict** with options: Choose Another Shortcut, Copy Binding Command, Skip Shortcut.
- If no Hyprland config can be read, status is `unknown`, not `available`.
- Vault never edits existing binding files. When installation arrives (M4), Vault writes its own file (`~/.config/hypr/omarchy-vault.conf`) and asks before adding a single `source =` line.
- Beam's bindings are treated like any other existing binding: never touched.

## Secrets hygiene

- No secrets in `config.json`. Secrets live in separate 0600 files.
- Logs contain method, path (tokens redacted) and status only; never query strings, bodies, cookies or headers.
- `.gitignore` excludes `*.env`, `*.token`, `*.key`, `*.pem`, `credentials*.json`.
