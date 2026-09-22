# API

Base URL: `http://127.0.0.1:8788`. Every endpoint is also available under `/api/v1/…`. Responses are JSON. Errors look like:

```json
{ "error": "not_found", "message": "Unknown API endpoint." }
```

Milestone 1 has no authentication because the service only listens on loopback and every endpoint is read-only. Authentication arrives in Milestone 3, before any write endpoint or remote access.

Requests must carry a loopback `Host` (`127.0.0.1`, `localhost`, `::1`) or a configured domain, otherwise `421`. Non-GET requests from another origin are refused with `403`.

## Available now (Milestone 1)

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

### `GET /api/storage`

Vault root status plus `candidates`: mounted, supported volumes that could become Vault storage.

```json
{ "configured": false, "root": "/srv/vault", "…": "…",
  "candidates": [ { "disk": "sda", "display_name": "WDC WD80EFZZ-68BTXN0", "volume": "sda1",
                    "mountpoint": "/mnt/wdred", "fstype": "ext4",
                    "size_bytes": 7875955240960, "free_bytes": 5276787884032, "health": "healthy" } ] }
```

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

## Planned (respond `501` today)

Each returns `{ "error": "not_implemented", "message": "…", "milestone": N }`.

| Method | Path | Milestone |
|---|---|---|
| POST | `/api/pool` | 2 |
| GET, POST | `/api/users` | 3 |
| POST | `/api/upload-session` | 4 |
| POST | `/api/download-session` | 5 |
| POST | `/api/share` | 5 |
| DELETE | `/api/share/{id}` | 5 |
| POST | `/api/remote` | 6 |

### Beam integration (reserved)

| Method | Path | Milestone |
|---|---|---|
| POST | `/api/v1/beam/upload-session` | 4 |
| POST | `/api/v1/beam/download-session` | 5 |
| POST | `/api/v1/beam/share` | 5 |

Planned response shape for sessions:

```json
{ "id": "ses_…", "url": "https://vault.example.com/u/<token>", "qr_svg": "<svg…>",
  "expires_at": "2026-09-22T19:40:00Z", "scope": "upload", "destination": "Phone Uploads" }
```

The token appears only in `url` and only once; Vault stores its hash. Destinations are Vault folder names, never filesystem paths.
