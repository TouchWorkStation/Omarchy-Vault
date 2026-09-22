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

5. **Install (Milestone 4, never automatic).** After the user confirms, Vault writes only its own file, `~/.config/hypr/omarchy-vault.conf`, and adds one `source = ~/.config/hypr/omarchy-vault.conf` line to the user's `hyprland.conf` after showing it. It never edits Omarchy's or Beam's binding files and never removes a binding. Uninstalling removes the Vault file and that one line.

## Check today

```sh
vaultctl shortcuts          # human-readable
vaultctl shortcuts --json
```

or `GET /api/shortcuts`, or Settings → Shortcuts in the dashboard.
