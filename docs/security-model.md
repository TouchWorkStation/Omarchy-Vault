# Security model (summary)

The full threat model is in [../SECURITY.md](../SECURITY.md). This page is the short version for contributors.

| Rule | Where it is enforced |
|---|---|
| Bind to loopback unless explicitly allowed | `config.ValidateListen`, `vaultd` startup |
| Reject unknown Host headers (DNS rebinding) | `api.hostGuard` |
| Refuse cross-site writes | `api.hostGuard` (Origin, Sec-Fetch-Site) |
| Strict CSP and security headers | `api.securityHeaders` |
| Rate limit | `api.rateLimiter` |
| No shell, allowlisted binaries only | `sysexec.System` |
| Device paths passed to tools are validated | `disks.validDevicePath` |
| System disk always protected, fail safe | `disks.Build` |
| Reserved mount locations never adoptable | `disks.isReservedTarget` |
| No destructive disk operations | not implemented anywhere, by policy; tests assert no such calls |
| Shortcuts: detect, report, never overwrite | `shortcuts.Inspector` |
| Writes need the 0600 local token or a session + intent header | `api.requireAuth`, `auth.Local` |
| Login codes single-use, 30 s; sessions HttpOnly SameSite=Strict | `auth.Local`, `api.handleLogin` |
| Adoption: fresh scan, name not path, os.Root, nested-mount check | `storage.Adopt` |
| Unplugged/moved drives never written to | `storage.Inspect` (UUID + mountinfo) |
| Config written atomically, 0600 | `config.Save` |
| Never replace real files/folders with links | `storage.SetLink`, `vaultctl link` |
| Tokens redacted from logs | `api.redactPath` |
| Secrets never in config.json or git | config schema, `.gitignore` |

When you add something that crosses a trust boundary (new binary in the allowlist, a new write operation, a new listener), update SECURITY.md in the same change.
