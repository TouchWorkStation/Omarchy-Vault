# Shortcuts

Vault uses the Vault's point of view:

| Shortcut | Action | Direction | Command |
|---|---|---|---|
| Super + Shift + V | Open Vault | | `vaultctl open` |
| Super + Shift + U | **Upload to Vault** | Phone → Vault | `vaultctl upload` |
| Super + Shift + D | **Download from Vault** | Vault → Phone | `vaultctl download` |

## Strategy

1. **Inspect.** Read live bindings with `hyprctl binds -j` (if Hyprland is running) and parse `~/.config/hypr/hyprland.conf` plus every file it `source`s (globs, `$variables`, `unbind`, submaps). Omarchy's defaults are sourced from `~/.local/share/omarchy/default/hypr/`, so they are included.
2. **Compare.** Modifiers are normalised (`SUPER_SHIFT`, `SUPER SHIFT`, `$mainMod SHIFT`, `WIN` → the same). Keys compare case-insensitively. NumLock/CapsLock bits are ignored, like Hyprland does. Submap bindings do not conflict with global ones.
3. **Report.** Each shortcut is `available`, `installed` (already bound to the Vault command), `conflict`, or `unknown` (no config found; never assumed free).
4. **On conflict**, show:

   ```
   Shortcut Conflict
   Super + Shift + D is already assigned.

   Options:
   - Choose Another Shortcut   (e.g. Super + Alt + D, checked to be free)
   - Copy Binding Command      bindd = SUPER SHIFT, D, Download from Vault, exec, vaultctl download
   - Skip Shortcut
   ```

5. **Install (never automatic).** After the user confirms, Vault writes only its own file, `~/.config/hypr/omarchy-vault.conf`, and adds one `source = ~/.config/hypr/omarchy-vault.conf` line to the user's `hyprland.conf` after showing it. It never edits Omarchy's or Beam's binding files and never removes a binding. Uninstalling removes the Vault file and that one line.

## Check

```sh
vaultctl shortcuts          # human-readable
vaultctl shortcuts --json
```

or `GET /api/shortcuts`, or Settings → Shortcuts in the dashboard.

## Install

```sh
vaultctl shortcuts install                    # shows what it will write, asks before writing
vaultctl shortcuts install --use-suggestions  # a taken shortcut gets its free alternative instead
vaultctl shortcuts install --yes              # no question (for scripts)
```

It installs only shortcuts whose command exists already (Download arrives in Milestone 5; run install again then) and only those that are free. A taken one is skipped, never overwritten, unless you pass `--use-suggestions`, which uses the checked-free alternative (e.g. Super + Alt + U). Hyprland reloads its config by itself, so they work at once.

What it writes:

- `~/.config/hypr/omarchy-vault.conf` (Vault's own file, marked with a header). If a file by that name exists without Vault's header, Vault refuses to touch it.
- one line at the end of `~/.config/hypr/hyprland.conf`, under a comment:

  ```
  # Omarchy Vault shortcuts (remove with: vaultctl shortcuts remove)
  source = ~/.config/hypr/omarchy-vault.conf
  ```

## Remove

```sh
vaultctl shortcuts remove
```

Deletes Vault's file and exactly those two lines; the rest of `hyprland.conf` stays byte for byte as it was. `scripts/uninstall.sh` does this for you.

## Prefer to do it by hand?

Add to `~/.config/hypr/bindings.conf`:

```
bindd = SUPER SHIFT, V, Open Vault, exec, vaultctl open
bindd = SUPER SHIFT, U, Upload to Vault, exec, vaultctl upload
```
