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
| `GET` status, storage, remote, files | open on loopback | any signed-in user |
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
  "remote": { "enabled": false, "state": "not_configured", "milestone": 6 },
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
- `mode`: `"single"` (default). `"combined"` returns `501` until Milestone 7.

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

### `GET /api/services`

Which supporting tools and units are present (read-only).

### `GET /api/remote`

`{ "enabled": false, "state": "not_configured", "milestone": 6 }`

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

#### `GET /api/activity`

`{ "items": [ …up to 20 recently received files… ] }`. Admins see all, others their own.

### Phone pages (port 8790, only while a link is active)

| Method | Path | |
|---|---|---|
| GET | `/u/{token}` | Mobile upload page (or "This link has ended") |
| GET | `/u/{token}/info` | `{ folder, expires_at (Unix ms), files_left }` |
| POST | `/u/{token}/files` | `multipart/form-data`, one or more `file` parts; returns `{ "saved": [{name, size}], "folder" }` |
| GET | `/t/…` | Page assets |

### Power

`POST /api/power/off` (admin): stops Vault (the dashboard's "Turn off Vault" button). `vaultctl off` does the same through systemd.

## Planned (respond `501` today)

Each returns `{ "error": "not_implemented", "message": "…", "milestone": N }`.

| Method | Path | Milestone |
|---|---|---|
| POST | `/api/download-session` | 5 |
| POST | `/api/share` | 5 |
| DELETE | `/api/share/{id}` | 5 |
| POST | `/api/remote` | 6 |

### Beam integration

`POST /api/v1/beam/upload-session` is live (Milestone 4): same body and response as `POST /api/upload-session`, authenticated with the local owner token in `X-Vault-Token`. The others are reserved:

| Method | Path | Milestone |
|---|---|---|
| POST | `/api/v1/beam/download-session` | 5 |
| POST | `/api/v1/beam/share` | 5 |

Planned response shape for sessions:

```json
{ "id": "ses_…", "url": "https://vault.example.com/u/<token>", "qr_svg": "<svg…>",
  "expires_at": "2026-09-22T19:40:00Z", "scope": "upload", "destination": "Phone Uploads" }
```

The token appears only in `url` and only once; Vault stores its hash. Destinations are Vault folder names, never filesystem paths.
