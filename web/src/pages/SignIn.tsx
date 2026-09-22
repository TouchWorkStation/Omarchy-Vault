import { useState } from "react";
import { ApiError, send } from "../api";
import { Notice } from "../components/ui";

export function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [totp, setTotp] = useState("");
  const [needTotp, setNeedTotp] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await send("POST", "/api/auth/login", { username, password, totp });
      const next = new URLSearchParams(window.location.search).get("next");
      // Only same-site paths; Files lives outside the app so do a full load.
      if (next && next.startsWith("/") && !next.startsWith("//")) {
        window.location.assign(next);
        return;
      }
      onSignedIn();
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") {
        setNeedTotp(true);
      } else {
        setError(err instanceof ApiError ? err.message : "Something went wrong.");
        if (err instanceof ApiError && err.code === "bad_totp") setTotp("");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="signin">
      <form className="card signin-card" onSubmit={submit}>
        <div className="brand signin-brand">
          <span className="brand-mark" aria-hidden="true">
            ▣
          </span>
          <span>VAULT</span>
        </div>
        <label className="field">
          <span>Username</span>
          <input
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={needTotp}
            required
            autoFocus
          />
        </label>
        <label className="field">
          <span>Password</span>
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={needTotp}
            required
          />
        </label>
        {needTotp && (
          <label className="field">
            <span>6-digit code from your authenticator app</span>
            <input
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9 ]{6,7}"
              value={totp}
              onChange={(e) => setTotp(e.target.value)}
              required
              autoFocus
            />
          </label>
        )}
        {error && <Notice tone="bad">{error}</Notice>}
        <button className="btn btn-primary btn-block" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
        <p className="muted small">On this computer you can also press Super+Shift+V.</p>
      </form>
    </div>
  );
}
