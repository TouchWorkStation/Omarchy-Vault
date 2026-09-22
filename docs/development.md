# Development

## Requirements

- Go 1.24+
- Node 22+ and npm
- Linux with `util-linux` (`lsblk`, `findmnt`). `smartmontools` optional.

## Run it

```sh
make demo      # sample drives + shortcuts: vaultd --demo on :8788, Vite UI on :5173
make dev       # same, but this machine's real drives (read-only)
```

Open http://127.0.0.1:5173 during development (hot reload; `/api` is proxied to vaultd), or http://127.0.0.1:8788 to use the UI embedded in the binary.

## Build and test

```sh
make all       # npm ci + vite build into web/dist, then go build into bin/
make test      # go test ./...
make check     # vet + tests + gofmt check + web typecheck/build
```

`web/dist` is embedded into `vaultd` with `go:embed`. If you build Go without building the web UI first, vaultd serves a short "UI not built" page and the API still works.

## CLI

```sh
./bin/vaultctl disks         # works without the daemon
./bin/vaultctl storage       # storage state + drives Vault can use
./bin/vaultctl setup         # opens /setup signed in (needs the daemon)
./bin/vaultctl disks --json
./bin/vaultctl shortcuts
./bin/vaultctl doctor
./bin/vaultd --demo          # or --no-smart, --listen 127.0.0.1:9000, --config path
VAULT_ADDR=http://127.0.0.1:9000 ./bin/vaultctl status
```

## Layout and conventions

- Safety-critical parsing is pure and tested with fixtures: `internal/disks/testdata`, `internal/health/testdata`, `internal/shortcuts/testdata`.
- All command execution goes through `internal/sysexec` (allowlist, no shell, timeout, `LC_ALL=C`). Tests use `sysexec.Fake`.
- API types in `web/src/api.ts` mirror the Go structs; update both together.
- The dashboard uses no external fonts, scripts or images (CSP is `default-src 'self'`).

## Demo mode

`vaultd --demo` serves sample drives. The sample "WD Red" and USB stick are real folders in a throwaway sandbox under `~/.cache/omarchy-vault/demo-*`, so you can click through setup and it really creates folders. Your real config, token and `/srv/vault` are never touched, and the sandbox is deleted when vaultd exits. Capacity shown after setup is the capacity of the disk holding the sandbox.

If your home directory is under a location Vault refuses as storage (for example `/root` in a container), point the sandbox elsewhere: `XDG_CACHE_HOME=/home/you/.cache ./bin/vaultd --demo`.

## Adding a fixture from a real machine

```sh
lsblk -J -b -o NAME,KNAME,PATH,PKNAME,TYPE,SIZE,MODEL,SERIAL,VENDOR,FSTYPE,UUID,LABEL,PARTLABEL,MOUNTPOINTS,FSSIZE,FSUSED,FSAVAIL,RM,RO,ROTA,TRAN,HOTPLUG > my_lsblk.json
findmnt -J -o TARGET,SOURCE,FSTYPE > my_findmnt.json
```

Scrub serial numbers and UUIDs before committing.
