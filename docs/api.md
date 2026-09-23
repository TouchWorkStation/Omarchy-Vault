# API

Base URL: `http://127.0.0.1:8788`. Every endpoint is also available under `/api/v1/…`. Responses are JSON. Errors look like:

```json
{ "error": "not_found", "message": "Unknown API endpoint." }
```

Authentication is one of:

- `X-Vault-Token: <contents of ~/.config/omarchy-vault/secrets/local-token>`: the local owner (CLI, Beam), acts as an admin.
- The `vault_session` cookie from a sign-in. Writes also need `X-Vault-Request: 1`.

| Endpoint group | Before the first account | After |
|---|---|---|
| `GET` status, storage, files | open on loopback | any signed-in user |
| `GET` disks, settings, shortcuts, services, users | open on loopback | admins |
| Writes (pool, users, account) | local token | admins (account: any signed-in user) |

Missing auth returns `401 login_required`; the wrong role returns `403 admin_only`.

Requests must carry a loopback `Host` (`127.0.0.1`, `localhost`, `::1`) or a configured domain, otherwise `421`. Non-GET requests from another origin are refused with `403`.

## Available now

### `GET /api/status`

Dashboard summary.

```json
{
  "name": "Omarchy Vault",
  "version": "0.1.0-dev",
  "milestone": 1,
  "hostname": "omarchy",
  "uptime_seconds": 42,
  "listen": "127.0.0.1:8788",
  "setup_complete": false,
  "storage": { "configured": false, "root": "/srv/vault", "root_exists": false, "pool_mode": "none",
               "sources": [], "total_bytes": 0, "used_bytes": 0, "free_bytes": 0,
               "message": "Vault storage has not been set up yet." },
  "drives": { "total": 7, "system": 1, "available": 2, "unmounted": 1, "unsupported": 3,
              "healthy": 2, "warning": 1, "critical": 0, "unknown": 4 },
  "system_disk_detected": true,
  "phone": { "active_links": 0, "listening": false },
  "auto_off_minutes": 15,
  "users": { "count": null, "milestone": 3 },
  "services": [ { "id": "smartctl", "name": "Drive health", "installed": true, "milestone": 1, "…": "…" } ],
  "shortcuts": [ { "id": "upload", "label": "Upload to Vault", "combo": "Super + Shift + U", "status": "available" } ],
  "warnings": []
}
```

### `GET /api/disks[?refresh=1]`

Drive inventory. Cached for 30 s; `refresh=1` forces a rescan at most every 5 s.

```json
{
  "system_disk_detected": true,
  "summary": { "total": 7, "system": 1, "available": 2, "…": "…" },
  "warnings": [],
  "scanned_at": "2026-09-22T19:30:00Z",
  "disks": [
    {
      "id": "nvme0n1", "name": "nvme0n1", "path": "/dev/nvme0n1",
      "display_name": "Samsung SSD 980 PRO 512GB", "kind": "nvme", "size_bytes": 512110190592,
      "removable": false, "read_only": false,
      "system": true, "protected": true,
      "protected_reason": "This drive runs Omarchy (/boot, /home, /). Vault will never use it for storage.",
      "status": "system", "adoptable": false,
      "health": { "status": "healthy", "smart_passed": true, "temperature_c": 38, "power_on_hours": 2200 },
      "volumes": [
        { "name": "nvme0n1p1", "path": "/dev/nvme0n1p1", "type": "part", "fstype": "vfat",
          "mountpoints": ["/boot"], "fs_size_bytes": 2143281152, "fs_avail_bytes": 1993281152,
          "status": "system", "adoptable": false, "notes": ["Used by the operating system."] }
      ]
    }
  ]
}
```

`status` (drive): `system`, `available`, `unmounted`, `unsupported`, `read-only`.
`status` (volume): `system`, `mounted`, `unmounted`, `container`, `unsupported`, `swap`.
`health.status`: `healthy`, `warning`, `critical`, `unknown` (with `reasons` or `message`).

### `GET /api/storage[?refresh=1]`

Live Vault storage state plus `candidates`: mounted, supported volumes that could become Vault storage.

```json
{
  "state": "ready",
  "configured": true,
  "root": "/srv/vault",
  "root_link": { "path": "/srv/vault", "state": "ok", "target": "/home/me/.local/share/omarchy-vault/current" },
  "data_link": "/home/me/.local/share/omarchy-vault/current",
  "pool_mode": "single",
  "sources": [ { "label": "WDC WD80EFZZ-68BTXN0", "volume": "sda1", "path": "/mnt/wdred", "folder": "Vault",
                 "data_dir": "/mnt/wdred/Vault", "state": "ready",
                 "total_bytes": 7875955240960, "used_bytes": 2599023255552, "free_bytes": 5276787884032 } ],
  "total_bytes": 7875955240960, "used_bytes": 2599023255552, "free_bytes": 5276787884032,
  "candidates": [ { "disk": "sda", "display_name": "WDC WD80EFZZ-68BTXN0", "volume": "sda1",
                    "mountpoint": "/mnt/wdred", "fstype": "ext4",
                    "size_bytes": 7875955240960, "free_bytes": 5276787884032, "health": "healthy" } ]
}
```

`state`: `not_set_up`, `ready`, `drive_missing` (unplugged or unmounted), `drive_moved` (`current_mount` says where), `problem` (with `message`).
`root_link.state`: `ok`, `missing` (run `vaultctl link`), `elsewhere`, `not_link` (Vault leaves those alone).

### `POST /api/pool` (auth)

Use one mounted drive as the Vault.

```json
{ "volume": "sda1", "folder": "Vault", "create_folders": true, "replace": false }
```

- `volume`: a name from `GET /api/disks`. Vault never accepts a path; it resolves the mount point from its own fresh scan.
- `folder`: omitted means `"Vault"`; `""` means the whole drive; up to four plain path components.
- `create_folders`: default `true`. Creates Photos, Documents, Backups, Projects, Phone Uploads, Shared if missing.
- `replace`: required when storage is already set up. Old files stay where they are.
- `mode`: `"single"` (default). `"combined"` returns `501` until Milestone 6.

Response: `{ "storage": <GET /api/storage without candidates>, "result": { "data_dir", "created": [], "existing": [], "skipped": [] }, "notes": [] }`.

| Status | `error` | Meaning |
|---|---|---|
| 400 | `bad_request`, `unsafe_path` | malformed body, unknown field, bad folder, symlink or nested mount |
| 401 | `login_required`, `bad_token` | not authorized |
| 403 | `system_disk` | the volume is on the system disk |
| 404 | `not_found` | no such volume now |
| 409 | `not_adoptable`, `unknown_system_disk`, `already_set_up` | not usable, system disk unknown, or `replace` missing |

### `DELETE /api/pool` (auth)

Stop using the current storage. Config and the `current` link are cleared; **no file is touched**.

### Signing in

| Method | Path | |
|---|---|---|
| POST | `/api/auth/login` | `{ "username", "password", "totp"? }` → `{ "user": … }` plus the `vault_session` cookie, and the Files cookie when Files runs. Errors: `bad_credentials` (401), `totp_required` (401), `bad_totp` (401), `locked` (429 + `Retry-After`) |
| GET | `/api/session` | `{ "signed_in", "can_change", "user"?, "local", "accounts_exist", "files_signed_in" }` |
| POST | `/api/logout` | needs `X-Vault-Request: 1`; clears both cookies |
| POST | `/api/local-login` | with `X-Vault-Token`; returns `{ "path": "/login?code=…", "expires_in": 30 }` |
| GET | `/login?code=…&next=/setup` | redeems the single-use code, sets `vault_session`, redirects to `next` (same-site paths only) |

### Users (admin)

| Method | Path | Body |
|---|---|---|
| GET | `/api/users` | → `{ "users": [UserView], "folders": ["Documents", "Photos", …] }` |
| POST | `/api/users` | `{ "username", "password", "role": "admin\|family\|guest", "folders"?: [{ "name", "access": "rw\|ro" }] }`. The first account is always an admin, needs the local token, and signs its creator in. |
| PUT | `/api/users/{name}` | `{ "role"?, "disabled"?, "folders"? }`. Disabling or changing role signs them out. |
| POST | `/api/users/{name}/password` | `{ "password" }`. Signs them out everywhere. |
| DELETE | `/api/users/{name}` | Removes the account (never files) |

`UserView`: `{ "username", "role", "disabled", "folders", "all_folders", "totp_enabled", "created_at" }`. Hashes and TOTP secrets are never returned.

Rules: usernames are `^[a-z][a-z0-9_-]{1,31}$`, passwords are 10–256 characters, and the last active admin cannot be disabled, demoted or removed (`409 last_admin`).

### Your account (any signed-in user)

| Method | Path | Body |
|---|---|---|
| POST | `/api/account/password` | `{ "current", "new" }` |
| POST | `/api/account/totp/setup` | → `{ "secret", "uri", "qr_svg" }` |
| POST | `/api/account/totp/enable` | `{ "code" }` |
| POST | `/api/account/totp/disable` | `{ "password" }` |

### Files

- `GET /api/files` → `{ "state": "not_installed|waiting_for_storage|starting|running|error", "installed", "running", "url": "/files/web/client/files", "message"?, "signed_in" }`
- `/files/*` is SFTPGo's web client behind Vault's sign-in. Without a session, `GET` redirects to `/signin?next=…`.

### `GET /api/settings`

Current configuration (no secrets are stored in it), plus `config_found`, `config_error`, `read_only: true`.

### `GET /api/shortcuts`

Shortcut plan and conflict analysis. See [shortcuts.md](shortcuts.md).

```json
{ "sources": ["hyprctl", "/home/me/.config/hypr/hyprland.conf"], "installed": false,
  "checks": [ { "id": "download", "label": "Download from Vault", "direction": "Vault → Phone",
                "combo": "Super + Shift + D", "status": "conflict",
                "binding_line": "bindd = SUPER SHIFT, D, Download from Vault, exec, vaultctl download",
                "conflicts": [ { "mods": ["SUPER","SHIFT"], "key": "D", "dispatcher": "exec",
                                 "arg": "omarchy-launch-tui lazydocker", "description": "Lazydocker",
                                 "source": "/home/me/.local/share/omarchy/default/hypr/bindings/tui.conf" } ],
                "suggestion": { "mods": ["SUPER","ALT"], "key": "D", "…": "…" },
                "options": ["choose_another", "copy_binding", "skip"] } ] }
```

### Shortcut editor (admin)

- `GET /api/shortcuts`: the conflict report for Vault's default keys, plus `current` (what is saved now), `mod_choices`, `key_choices` and `file`.
- `POST /api/shortcuts/check` `{ "bindings": [{ "id": "upload", "mods": ["SUPER","ALT"], "key": "U" }] }`: checks without changing anything. Each result is `available`, `conflict` (with `conflicts` and a free `suggestion`), `duplicate` or `invalid`.
- `POST /api/shortcuts` (same body): saves them. Refused with `409` unless every one is free; an empty list removes them.
- `DELETE /api/shortcuts`: removes Vault's shortcuts and its `source` line.

`id` is one of `open`, `upload`, `download`; the command is fixed per id. Modifiers are SUPER/SHIFT/CTRL/ALT (at least one of SUPER, CTRL, ALT); keys A–Z, 0–9, F1–F12.

### `GET /api/services`

Which supporting tools and units are present (read-only).

### Upload to Vault (signed in; admin, or family into a folder they can write)

#### `POST /api/upload-session`

Body (all optional): `{ "folder": "Phone Uploads", "minutes": 10, "client": "dashboard" }`. `folder` is a Vault folder name, never a path; it defaults to `preferences.upload_folder`. `minutes` defaults to `preferences.upload_expiry_minutes` and is capped at 60. Starts the phone listener if it isn't running.

```json
{ "id": "h8Jh0-pT0yE_G4P-", "kind": "upload", "folder": "Phone Uploads", "created_by": "chris",
  "client": "dashboard", "created_at": "…", "expires_at": "…", "revoked": false,
  "max_files": 1000, "max_bytes": 107374182400, "files": 0, "bytes": 0,
  "state": "active", "url": "http://192.168.1.20:8790/u/<token>", "qr_svg": "<svg…>", "received": [] }
```

Errors: `400 bad_folder`, `403 not_allowed`, `409 storage_not_ready`, `503 no_network` (no private LAN address, or port 8790 in use).

The token is only inside `url`. Vault keeps the URL in memory so the QR can be shown again while it runs; after a restart the link still works from a phone, but its QR can't be shown again.

#### `GET /api/upload-sessions`

`{ "links": [ …active links, as above… ], "listening": true }`. Admins see every link, others their own.

#### `GET /api/upload-session/{id}` · `DELETE /api/upload-session/{id}`

One link with its `state` (`active`, `expired`, `stopped`, `full`) and the files `received` so far (`[{ "name", "folder", "size", "at", "actor", "kind" }]`). `DELETE` stops the link immediately.

### Download from Vault and share links

#### `GET /api/browse?path=Photos/2024`

One Vault folder for the picker: `{ "path", "entries": [{ "name", "path", "dir", "size", "modified" }], "truncated" }`. Without `path`, the top level (only folders you can open). Hidden files, symlinks and unfinished uploads are never listed.

#### `POST /api/download-session` (signed in; any account with access to the folder)

Body: `{ "path": "Photos/2024/beach.jpg", "minutes": 10, "max_downloads": 1, "client": "dashboard" }`. `path` is a file or folder inside the Vault (a folder downloads as `<name>.zip`). `minutes` 1–60 (default `preferences.download_expiry_minutes`), `max_downloads` 1–10 (default `preferences.download_max_count`). Returns the same link view as uploads, with `kind: "download"`, `path` and a `/d/<token>` URL.

Errors: `404 not_found` (no such file, or not yours to read), `400 not_plain` (a symlink or special file), `400 too_many_files` (folder with more than 20 000 files), `409 storage_not_ready`, `503 no_network`.

#### `POST /api/share` (admin, or family for folders they can open)

Body: `{ "path": "Photos/2024", "minutes": 60, "max_downloads": 0, "password": "" }`. `minutes` up to 1440 (24 h, default 1 h); `max_downloads` 0 = unlimited until expiry; `password` optional (account password rules). Returns a link view with `kind: "share"`, `has_password` and a `/s/<token>` URL. The password and its hash are never returned.

#### Listing and stopping

| Method | Path | |
|---|---|---|
| GET | `/api/download-sessions` · `/api/shares` | `{ "links": [...], "listening" }`: active links (admins see all, others their own) |
| GET · DELETE | `/api/download-session/{id}` · `/api/share/{id}` | One link; `DELETE` stops it |
| GET · DELETE | `/api/link/{id}` | The same for a link of any kind (used by the QR window) |

For downloads and shares `files`/`max_files` count downloads, and `received` lists what was downloaded. `max_files` of 2 147 483 647 means unlimited.

#### `GET /api/activity`

`{ "items": [ …up to 20 recent transfers, both directions (kind upload / download / share)… ] }`. Admins see all, others their own.

### Phone pages (port 8790, only while a link is active)

| Method | Path | |
|---|---|---|
| GET | `/u/{token}` | Mobile upload page (or "This link has ended") |
| GET | `/u/{token}/info` | `{ folder, expires_at (Unix ms), files_left }` |
| POST | `/u/{token}/files` | `multipart/form-data`, one or more `file` parts; returns `{ "saved": [{name, size}], "folder" }` |
| GET | `/d/{token}` | Mobile download page: name, size, Download button |
| GET | `/d/{token}/file` | The file (Range supported) or the folder as `.zip` |
| GET | `/s/{token}` | Share page (or its password form); folders list their files |
| POST | `/s/{token}/unlock` | Form field `password`; sets an HttpOnly cookie scoped to this link, then redirects back |
| GET | `/s/{token}/file[?p=sub/path]` | One file from the share |
| GET | `/s/{token}/zip` | A shared folder as `.zip` |
| GET | `/t/…` | Page assets |

### Power

`POST /api/power/off` (admin): stops Vault (the dashboard's "Turn off Vault" button). `vaultctl off` does the same through systemd. Vault also stops by itself after `auto_off_minutes` (default 15) with no API use, no active link and no transfer in progress.

### Beam integration

Live, authenticated with the local owner token in `X-Vault-Token`, same bodies and responses as Vault's own endpoints:

| Method | Path | Same as |
|---|---|---|
| POST | `/api/v1/beam/upload-session` | `POST /api/upload-session` |
| POST | `/api/v1/beam/download-session` | `POST /api/download-session` |
| POST | `/api/v1/beam/share` | `POST /api/share` |

Beam should send `"client": "beam"` so links show where they came from.

The response shape, trimmed:

```json
{ "id": "QVFqlxMjFxB9kaNc", "kind": "download", "folder": "Photos", "path": "Photos/2024/beach.jpg",
  "expires_at": "2026-09-22T19:40:00Z", "max_files": 1, "files": 0, "has_password": false,
  "state": "active", "url": "http://192.168.1.20:8790/d/<token>", "qr_svg": "<svg…>", "received": [] }
```

The token appears only in `url`; Vault stores its hash. Paths are Vault paths (`Photos/2024/beach.jpg`), never filesystem paths.
