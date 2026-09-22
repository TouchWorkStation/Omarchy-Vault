import { useState } from "react";
import { ApiError, send, type Session } from "../api";
import { Badge, Card, Loading, Notice } from "../components/ui";
import { useApi } from "../useApi";

function PasswordCard() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [msg, setMsg] = useState<{ tone: "info" | "bad"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (next !== confirm) {
      setMsg({ tone: "bad", text: "The new passwords don't match." });
      return;
    }
    setBusy(true);
    try {
      await send("POST", "/api/account/password", { current, new: next });
      setMsg({ tone: "info", text: "Password changed. Other devices have been signed out." });
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (err) {
      setMsg({ tone: "bad", text: err instanceof ApiError ? err.message : "Something went wrong." });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="PASSWORD">
      <form className="form" onSubmit={submit}>
        <label className="field">
          <span>Current password</span>
          <input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
        </label>
        <label className="field">
          <span>New password (10+ characters)</span>
          <input type="password" autoComplete="new-password" minLength={10} value={next} onChange={(e) => setNext(e.target.value)} required />
        </label>
        <label className="field">
          <span>Repeat new password</span>
          <input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        </label>
        {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
        <div className="setup-actions">
          <button className="btn btn-primary" disabled={busy}>
            Change password
          </button>
        </div>
      </form>
    </Card>
  );
}

function TwoFactorCard({ enabled, onChange }: { enabled: boolean; onChange: () => void }) {
  const [setup, setSetup] = useState<{ secret: string; qr_svg: string } | null>(null);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [msg, setMsg] = useState<{ tone: "info" | "bad"; text: string } | null>(null);

  async function start() {
    setMsg(null);
    try {
      setSetup(await send("POST", "/api/account/totp/setup"));
    } catch (err) {
      setMsg({ tone: "bad", text: err instanceof ApiError ? err.message : "Something went wrong." });
    }
  }
  async function enable(e: React.FormEvent) {
    e.preventDefault();
    try {
      await send("POST", "/api/account/totp/enable", { code });
      setSetup(null);
      setMsg({ tone: "info", text: "Two-factor sign-in is on. You'll need a code each time you sign in." });
      onChange();
    } catch (err) {
      setMsg({ tone: "bad", text: err instanceof ApiError ? err.message : "Something went wrong." });
    }
  }
  async function disable(e: React.FormEvent) {
    e.preventDefault();
    try {
      await send("POST", "/api/account/totp/disable", { password });
      setPassword("");
      setMsg({ tone: "info", text: "Two-factor sign-in is off." });
      onChange();
    } catch (err) {
      setMsg({ tone: "bad", text: err instanceof ApiError ? err.message : "Something went wrong." });
    }
  }

  return (
    <Card title="TWO-FACTOR SIGN-IN" actions={enabled ? <Badge tone="good" glyph="●">On</Badge> : <Badge tone="muted" glyph="○">Off</Badge>}>
      {!enabled && !setup && (
        <>
          <p className="muted">Ask for a 6-digit code from an authenticator app (Aegis, 2FAS, Google Authenticator, 1Password…) when signing in.</p>
          <div className="setup-actions">
            <button className="btn btn-primary" onClick={start}>
              Set up two-factor
            </button>
          </div>
        </>
      )}
      {setup && (
        <form className="form totp-setup" onSubmit={enable}>
          <img className="qr" alt="QR code for your authenticator app" src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(setup.qr_svg)}`} />
          <div>
            <p>1. Scan this code with your authenticator app.</p>
            <p className="muted small">
              Can't scan? Enter this key: <code className="secret">{setup.secret.replace(/(.{4})/g, "$1 ").trim()}</code>
            </p>
            <label className="field">
              <span>2. Enter the 6-digit code it shows</span>
              <input inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} required />
            </label>
            <div className="setup-actions">
              <button type="button" className="btn" onClick={() => setSetup(null)}>
                Cancel
              </button>
              <button className="btn btn-primary">Turn on</button>
            </div>
          </div>
        </form>
      )}
      {enabled && (
        <form className="form" onSubmit={disable}>
          <label className="field">
            <span>To turn it off, enter your password</span>
            <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          </label>
          <div className="setup-actions">
            <button className="btn btn-danger">Turn off two-factor</button>
          </div>
        </form>
      )}
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
    </Card>
  );
}

export function Account() {
  const { data: session, reload } = useApi<Session>("/api/session");
  if (!session) return <Loading what="your account" />;
  if (session.local || !session.user) {
    return (
      <section className="page">
        <h1>ACCOUNT</h1>
        <Notice>
          You opened Vault from this computer without signing in. Sign out and sign in with your Vault account to change your password
          or two-factor settings.
        </Notice>
      </section>
    );
  }
  const u = session.user;
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>ACCOUNT</h1>
          <p className="subtitle">
            {u.username} · {u.role}
          </p>
        </div>
      </header>
      <div className="two-col">
        <PasswordCard />
        <TwoFactorCard enabled={u.totp_enabled} onChange={reload} />
      </div>
    </section>
  );
}
