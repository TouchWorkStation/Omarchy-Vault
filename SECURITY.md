# Security

Vault stores private files. Security and data safety come before features.

**Reporting a vulnerability:** please open a private security advisory on GitHub (Security → Report a vulnerability) rather than a public issue.

## Threat model

### Assets

1. The user's files in the Vault and on attached drives.
2. The operating system disk and its data.
3. Credentials: user passwords, TOTP secrets, SFTPGo admin credentials, the local owner token.
4. Transfer and share tokens.
5. The integrity of the user's desktop configuration (Hyprland bindings, systemd units).

### Adversaries

| Adversary | Example | Primary controls |
|---|---|---|
| Malicious website in the user's browser | DNS rebinding to 127.0.0.1, cross-site POST | Host allowlist, Origin / Sec-Fetch-Site checks, SameSite cookies, CSP |
| Someone on the LAN | Scanning for open services | Dashboard on loopback only; the phone port exists only while a link is active and serves only token pages; SMB/SFTP off |
| Someone on the internet | Probing for the machine | Nothing is exposed: no tunnel, no VPN, no router ports. The phone port binds to the private LAN address only |
| Holder of a leaked QR/share link | Photo of a screen, forwarded message | Short expiry, single scope, max uses, revocation, read-only/upload-only |
| Authenticated but limited user (Guest) | Reading another user's folder | SFTPGo per-user virtual folders and permissions, path validation |
| Buggy Vault code | Wrong disk chosen, path traversal | Read-only M1, fail-safe system disk detection, no formatting ever, tests |

### Out of scope

A root-level attacker on the machine, physical theft of unencrypted drives (use LUKS), and an attacker who controls your Wi-Fi (the phone port is plain HTTP; see "The phone listener").

## Trust boundaries

```
 Phone on the same Wi-Fi ──► phone listener (LAN:8790, only while a link is active, token pages only)
                                              │
 Browser on this machine ─────────────┬──► vaultd (user)  ──► privileged helper (root, allowlist)
 vaultctl / plugin / Beam ────────────┘       │                     │
                                              ▼                     ▼
                                       user's files          mounts, services, SMART
```

1. **Network → vaultd.** Everything from the network is untrusted. vaultd binds to `127.0.0.1:8788`. Listening elsewhere requires `security.allow_non_loopback_listen: true` in config.
2. **vaultd → system.** vaultd runs as the desktop user and can only execute an allowlisted set of binaries (`internal/sysexec`): `lsblk`, `findmnt`, `blkid`, `smartctl`, `hyprctl`, `systemctl`. No shell is ever used. Arguments are fixed in code; the only discovered value passed as an argument (a device path for `smartctl`) must match `^/dev/[a-z0-9_-]+$`.
3. **Other local users → vaultd.** Anyone on the machine can reach `127.0.0.1:8788`. Before the first account exists they can only read; afterwards they need a Vault account (see "Accounts and sign-in").
4. **vaultd → SFTPGo.** A child process on 127.0.0.1:8789, managed through its admin REST API (see "File service trust boundary").
5. **vaultd → privileged helper** (planned, Milestone 6). See below.

## Off by default

Vault runs only when the user turns it on (`vaultctl on`, `vaultctl open`, the Super+Shift+V shortcut). The systemd user unit deliberately has no `[Install]` section, so it cannot start at login or boot, and the installer removes start-at-login from earlier versions. `vaultctl off` or the dashboard's **Turn off Vault** (admins only) stops vaultd and its file service; nothing keeps listening. The smallest attack surface is a service that isn't running.

## Accounts and sign-in (Milestone 3)

- **Accounts** live in `~/.config/omarchy-vault/users.json` (0600). Each has an argon2id hash (46 MiB, t=1, p=1, 16-byte salt, PHC format). Plaintext passwords are never stored or logged. Hashing runs at most two at a time, to bound memory.
- **Sign-in** (`POST /api/auth/login`):
  - A wrong password and an unknown username give the same answer, and take the same time (a dummy hash is verified for unknown users).
  - After 5 failures for a username *or* client address, sign-in for it pauses for 1 minute, doubling up to 15 minutes. Success clears the pause.
  - Disabled accounts cannot sign in.
- **Two-factor:** optional TOTP (RFC 6238, SHA-1, 6 digits, ±30 s). The last accepted time step is stored, so a code can never be reused. Turning it off needs the password.
- **Sessions:**
  - A new random session id is issued on every sign-in (no fixation). Only its hash is kept server-side.
  - The cookie is HttpOnly and SameSite=Strict, lasts 12 hours, and becomes Secure over TLS.
  - Every request re-checks the account. A disabled, deleted or role-changed user loses access on their next request.
  - A password change or reset signs the user out everywhere.
- **CSRF:** cookie-authenticated writes must carry `X-Vault-Request: 1`. That custom header forces a CORS preflight, which Vault never approves. It comes on top of the Origin, Sec-Fetch-Site and Host checks and SameSite=Strict.
- **Roles** are enforced server-side on every endpoint:
  - Admin: everything.
  - Family and guest: status, their own account, and Files.
- **Last admin:** Vault refuses to disable, demote or delete the last active admin.
- **Before the first account exists**, reads are open on loopback (for first-run setup) and writes need the local token. Afterwards, everything except sign-in needs a session.

### Local owner access

The local token (`~/.config/omarchy-vault/secrets/local-token`: 32 random bytes, 0600 in a 0700 folder) identifies the desktop owner:

- `vaultctl` sends it as `X-Vault-Token` and acts as an admin. This is how you recover a forgotten admin password: `vaultctl users reset-password <name>`.
- `vaultctl open` exchanges it for a single-use, 30-second login code, then opens `/login?code=…`. The browser gets a session and never sees the token.
- Other Unix users on the machine can reach 127.0.0.1:8788 but cannot read the token, so they cannot change anything and, once accounts exist, cannot read anything either.
- Known limit: the login code appears on `xdg-open`'s command line for a moment. It works once, and only within 30 seconds.
- Demo mode (`vaultd --demo`) skips sign-in until its sandbox has an account; it never touches real config.

## File service (SFTPGo) trust boundary (Milestone 3)

- **Build:** SFTPGo v2.7.6 is built from the official repository, at a tag that must resolve to commit `62ae9ba3957e9ed52b44a4f885e805e2d7b35972`. The build refuses anything else. It installs into the user's home folder; no root is involved.
- **Process:** vaultd runs it as a child process, as the same user, with `Pdeathsig`. It is stopped whenever storage is not ready. Its configuration comes only from environment variables set by Vault:
  - HTTP listens on 127.0.0.1:8789 only.
  - Web admin, OpenAPI, SFTP, FTP, WebDAV and telemetry are off.
- **Admin credentials:** SFTPGo's admin password and JWT signing key are random, stored 0600 in `secrets/`, and never shown or logged. SFTPGo's own error lines are relayed to Vault's log. Its per-request access lines are not.
- **Users:** Vault mirrors each account into SFTPGo by sending the argon2id *hash*, never the password.
  - Family and guest users get an empty private home, plus virtual folders for exactly the folders granted.
  - Permissions are explicit lists (`list, download, upload, overwrite, delete, rename, create_dirs`): no symlink creation, chmod, chown or chtimes.
  - Guests get only `list, download` and write-disabled.
  - SSH, FTP and WebDAV are denied per user.
  - Web-client password change, password reset, MFA, API keys and shares are disabled; Vault owns those.
  - SFTPGo users Vault did not create are never modified or deleted.
- **Proxy:** Files is reached only through `/files/` on Vault. The proxy requires a Vault session, so Vault's sign-in, lockout and 2FA protect Files as well.
  - Only SFTPGo's own `jwt` cookie is forwarded upstream. Vault's session cookie and headers are stripped.
  - At sign-in, Vault signs the user into the web client on their behalf. The resulting cookie is HttpOnly, SameSite=Strict and scoped to `/files/web/client`, and SFTPGo binds it to 127.0.0.1.
- **Offline drive:** SFTPGo's home paths go through the data link. When the drive is missing, Vault removes the link and stops SFTPGo, so nothing can be written to the system disk.

## Privileged helper model

vaultd itself never needs root.

**`/srv/vault` is handled without a runtime helper.** It is a symlink created once by the installer or `vaultctl link`. Both show the exact `sudo ln -sn` command and ask first, and neither replaces anything that already exists. The symlink points to `~/.local/share/omarchy-vault/current`, a link owned by the user that vaultd switches atomically. If the drive disappears, the chain dangles instead of landing on the system disk.

Some later actions still need root: reading SMART on some SATA drives, enabling system services, and possibly pool mounts. These will be handled by `vault-helper`, a separate small binary run by systemd as root. It will be reachable only through a UNIX socket owned by the Vault user (mode 0600, peer credentials checked).

It exposes a fixed set of operations, each with validated, typed arguments:

| Allowed | Arguments validated against |
|---|---|
| `list_disks()` | none |
| `smart_status(device)` | device must exist in the current lsblk inventory |
| `mount_pool(sources)` | each source must be an adoptable mounted volume, not on the system disk |
| `unmount_pool()` | only Vault's own pool mount |
| `service_status(name)` / `enable_service(name)` | name from a fixed list: `sftpgo`, `smb` |

Forbidden, permanently: `exec(command)`, `run_shell(command)`, arbitrary paths, arbitrary unit names, formatting, partitioning, `wipefs`, `mkfs`, `dd`, `fdisk`/`parted`, editing `/etc/fstab` outside a Vault-owned, clearly marked block.

**The web UI never exposes sudo or shell execution.** There is no endpoint that accepts a command.

## Token model (Milestones 4–5)

Uploads since Milestone 4; downloads and share links since Milestone 5.

- 32 bytes from `crypto/rand`, base64url in the URL. Only the SHA-256 hash is stored (SQLite). Lookup compares hashes in constant time.
- Every token has one scope: upload into one folder, or download one file/folder, or view one share.
- Upload tokens: upload-only, cannot list or read; default 10-minute expiry (at most 60); 1000 files and 100 GB per link; revocable with Stop. Only admins, and family members into folders they can write, can create one; guests cannot.
- Wrong tokens count towards the same lockout as passwords (per phone IP, 1 → 15 minutes).
- Download tokens: one file or folder; default 10 minutes and 1 download (at most 60 minutes and 10 downloads); Stop at any time.
- A download is counted once per phone (network address) and file, when the transfer starts. The same phone can resume or retry an interrupted download until the link expires without using it up; another phone cannot. Counting is by address because phones have no account; two phones behind one address (unusual on home Wi-Fi) count as one.
- Share links: read only, always; 10 minutes to 24 hours (default 1 hour); one, limited or unlimited downloads until expiry; optional password; Stop at any time. Only admins, and family members for folders they can open, can create one; guests cannot.
- Share passwords are stored as argon2id hashes and follow the account password rules. Wrong guesses count towards a per-address lockout (1 → 15 minutes). A correct password gives an HttpOnly, SameSite=Lax cookie scoped to that one link's path, valid for at most 12 hours and never longer than the link; grants live in memory only.
- What a link can reach: exactly the file or folder it names, resolved inside the Vault through `os.Root`. Every path element is checked with `lstat` and a symlink anywhere is refused; files are opened with `O_NOFOLLOW`. Inside a shared folder, symlinks, special files and unfinished uploads are neither listed nor zipped, and a sub-path can never leave the folder (`..` is rejected).
- Pages served to phones never contain filesystem paths, only names relative to what was shared.
- URLs never contain filesystem paths. Filenames from phones are sanitised (no separators, no leading dots, no control characters, length-limited) and never overwrite existing files.
- Token path segments (`/u/…`, `/d/…`, `/s/…`) are redacted in logs.

### The phone listener (Milestone 4)

Phones can't reach the dashboard (it listens on 127.0.0.1 only). While at least one link (upload, download or share) is active, Vault opens a **second, separate** listener on your LAN address, port 8790. It serves only pages for a valid token (`/u/<token>` upload, `/d/<token>` download, `/s/<token>` share) and static page assets. No dashboard, no API, no Files, and no browsing beyond what one link names. It closes as soon as the last link expires, is used up or is stopped (checked every 30 seconds; a transfer still in progress finishes first), and it never runs while Vault is off. A share link keeps it open for as long as the share lasts (at most 24 hours, and only while Vault is on); stop shares you no longer need.

- It binds to one address, never `0.0.0.0`: the private LAN address it detects, or the IP you set as `transfer.host` in config (loopback and `0.0.0.0` are refused). If no private network address is found, no link is created.
- **Plain HTTP.** On your own Wi-Fi this is like any home device; someone on the same network who can capture traffic could see the token and the files. Don't use transfer or share links on untrusted Wi-Fi (cafés, hotels).
- Strict CSP (`default-src 'none'`, own scripts and styles only), `Referrer-Policy: no-referrer` (the token is in the URL), `no-store`, framing denied.
- Files are streamed to a hidden `.vault-partial-*` file inside the destination folder and renamed into place with `RENAME_NOREPLACE`, so an existing file is never replaced; the folder is opened through `os.Root`, so a symlink can't redirect the write. Partial files are deleted on error.

## Web security

Implemented in Milestone 1 (`internal/api/middleware.go`, tested):

- **Host allowlist**: `localhost`, `127.0.0.1`, `::1`, plus configured domains. Others get 421. Blocks DNS rebinding.
- **CSRF**: non-GET requests with a foreign `Origin`, or `Sec-Fetch-Site: cross-site`, are refused. Session cookies are `HttpOnly` and `SameSite=Strict`, and cookie-authenticated writes also require the `X-Vault-Request` header.
- **Headers**: strict CSP (`default-src 'self'`, no inline script, `frame-ancestors 'none'`), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, restrictive `Permissions-Policy`, COOP/CORP same-origin, `Cache-Control: no-store` on the API.
- **Rate limiting**: per-client token bucket on all requests; stricter limits for login and token endpoints from M3/M4.
- **Limits**: 1 MB API bodies, 64 KB headers, read/write timeouts.
- **Static files** are served from the embedded build only; there is no path from a URL to the host filesystem.

Authentication (M3) is described in "Accounts and sign-in" above.

## Path safety (from Milestone 3)

- All user-supplied paths are resolved relative to a Vault root, cleaned, and rejected if they contain `..`, NUL, or absolute components.
- Final paths are checked after resolving symlinks (`openat2` with `RESOLVE_BENEATH` where available, `filepath.EvalSymlinks` + prefix check otherwise) to prevent symlink escapes.
- Downloads of folders stream an archive with relative names only.

## Local only, short-lived

- No remote access of any kind: no Cloudflare Tunnel, no VPN, no router port forwarding, no cloud relay. Phones reach Vault only on the same network.
- Vault runs only after you turn it on (`vaultctl on`, a shortcut, or opening it); the systemd unit has no `[Install]` section, so it never starts at login or boot.
- **Auto-off**: after `auto_off_minutes` (default 15) with no dashboard or API use, no active link and no transfer in progress, Vault shuts itself down, taking the file service and the phone port with it. `0` disables this.

## Storage safety

- v0.1 never formats, partitions, erases filesystems, rewrites partition tables or modifies the root disk.
- Only already-mounted, supported filesystems (ext4, xfs, btrfs, f2fs; exFAT/NTFS/FAT with warnings) are adoptable.
- Adoption (Milestone 2) runs these checks, in order:
  1. The client sends only a volume name. vaultd looks it up on a **fresh** scan, never on data from the browser.
  2. It refuses the drive if the system disk is unknown, the drive is the system disk, or the volume is not adoptable.
  3. It confirms the mount in `/proc/self/mountinfo`.
  4. It creates folders through `os.Root`, which cannot follow symlinks out of the drive. Existing symlinks and files are refused and left untouched.
  5. It checks that the resulting folder resolves to itself and sits on the adopted filesystem, not on a mount nested inside it.
- At every status check, a configured source must match its filesystem UUID and be a real mount point. A drive that is unplugged, unmounted or moved is reported and never written to.
- "Stop using this drive" edits config only. Files are never moved or deleted.
- `config.json` is written atomically: a 0600 temp file, fsync, then rename. Invalid configs are never written.
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
