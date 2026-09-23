# Shortcuts

Vault uses the Vault's point of view:

| Shortcut | Action | Direction | Command |
|---|---|---|---|
| Super + Alt + V | Open Vault | | `vaultctl open` |
| Super + Alt + U | **Upload to Vault** | Phone → Vault | `vaultctl upload` |
| Super + Alt + D | **Download from Vault** | Vault → Phone | `vaultctl download` |

## Strategy

1. **Inspect.** Read live bindings with `hyprctl binds -j` (if Hyprland is running) and parse `~/.config/hypr/hyprland.conf` plus every file it `source`s (globs, `$variables`, `unbind`, submaps). Omarchy's defaults are sourced from `~/.local/share/omarchy/default/hypr/`, so they are included.
2. **Compare.** Modifiers are normalised (`SUPER_SHIFT`, `SUPER SHIFT`, `$mainMod SHIFT`, `WIN` → the same). Keys compare case-insensitively. NumLock/CapsLock bits are ignored, like Hyprland does. Submap bindings do not conflict with global ones.
3. **Report.** Each shortcut is `available`, `installed` (already bound to the Vault command), `conflict`, or `unknown` (no config found; never assumed free).
4. **On conflict**, show:

   ```
   Shortcut Conflict
   Super + Alt + D is already assigned.

   Options:
   - Choose Another Shortcut   (e.g. Super + Alt + D, checked to be free)
   - Copy Binding Command      bindd = SUPER ALT, D, Download from Vault, exec, vaultctl download
   - Skip Shortcut
   ```

5. **Install (never automatic).** After the user confirms, Vault writes only its own file, `~/.config/hypr/omarchy-vault.conf`, and adds one `source = ~/.config/hypr/omarchy-vault.conf` line to the user's `hyprland.conf` after showing it. It never edits Omarchy's or Beam's binding files and never removes a binding. Uninstalling removes the Vault file and that one line.

## Choose your own keys (dashboard)

**Settings → Keyboard shortcuts** lets you try different keys for each action:

1. Tick the actions you want. Pick modifiers (Super, Shift, Ctrl, Alt) and a key (A–Z, 0–9, F1–F12).
2. Each choice is checked against your Hyprland bindings as you go: **Free**, **Taken** (with who uses it and a one-click free alternative), or **Used twice**.
3. **Save shortcuts** writes them (only if every one is free) and Hyprland picks them up at once. **Remove Vault's shortcuts** takes them all out again.

Only Vault's own file and its one `source` line are ever written. The browser sends only which action and which keys; the command for each action is fixed inside Vault, and keys are limited to the list above, so nothing else can end up in your Hyprland config. At least one of Super, Ctrl or Alt is required so a shortcut can't fire while you type.

Super shortcuts can't be tried inside the page itself (Hyprland handles them before the browser sees them): save, then press them.

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

It installs only shortcuts whose command exists already and only those that are free. If you installed them before Milestone 5, run it again to add the Download shortcut. A taken one is skipped, never overwritten, unless you pass `--use-suggestions`, which uses the checked-free alternative (e.g. Super + Alt + U). Hyprland reloads its config by itself, so they work at once.

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
bindd = SUPER ALT, V, Open Vault, exec, vaultctl open
bindd = SUPER ALT, U, Upload to Vault, exec, vaultctl upload
bindd = SUPER ALT, D, Download from Vault, exec, vaultctl download
```
