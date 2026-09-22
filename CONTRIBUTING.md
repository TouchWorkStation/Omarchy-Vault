# Contributing

Thanks for helping build Omarchy Vault.

## Ground rules

1. **Data safety first.** No code may format, partition, wipe, or rewrite partition tables. Changes near drive handling need tests that prove the system disk stays protected.
2. **No shell from the web.** Never add an endpoint that runs a command. New system tools go through `internal/sysexec` and its allowlist; adding to it is a security change and must say so in the PR.
3. **Never overwrite user config.** Hyprland bindings, fstab, systemd units owned by others: read, report, ask.
4. **Plain language in the UI.** "Combine these drives into one Vault", not "Configure mergerfs". Advanced terms belong in advanced settings.
5. **Stay light.** Prefer the standard library; justify every new dependency.
6. **Beam is separate.** Do not import, modify or depend on Beam.

## Workflow

```sh
make check        # go vet, go test, gofmt check, web typecheck + build
make demo         # vaultd --demo + Vite dev server
```

- Keep commits focused, with clear messages.
- Add or update tests with every behaviour change. Parsing and safety logic should be pure functions tested with fixtures in `testdata/`.
- Update docs (`docs/api.md`, `SECURITY.md`) when you change an API or a trust boundary.
- UI: dark theme tokens in `web/src/styles.css`; status is never shown by color alone (glyph + word).

## Branches

`main` is stable. Work on feature branches and open a pull request. Do not force-push shared branches.
