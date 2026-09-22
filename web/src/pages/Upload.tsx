import { useState } from "react";
import { ApiError, send, type LinkView, type Session, type UsersResponse } from "../api";
import { navigate } from "../App";
import { Card, Kbd, Loading, Notice } from "../components/ui";
import { useApi } from "../useApi";

export function Upload() {
  const { data: session } = useApi<Session>("/api/session");
  const { data: links, reload } = useApi<{ links: LinkView[] }>("/api/upload-sessions", 5000);
  const isAdmin = session?.can_change;
  const { data: usersInfo } = useApi<UsersResponse>(isAdmin ? "/api/users" : "/api/session");
  const [folder, setFolder] = useState("");
  const [minutes, setMinutes] = useState(10);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!session) return <Loading what="Upload" />;

  // Folders this user can upload into.
  const choices: string[] = isAdmin
    ? ((usersInfo as UsersResponse | null)?.folders ?? [])
    : (session.user?.folders.filter((f) => f.access === "rw").map((f) => f.name) ?? []);
  const canUpload = isAdmin || (session.user?.role === "family" && choices.length > 0);

  async function create() {
    setBusy(true);
    setError(null);
    try {
      const link = await send<LinkView>("POST", "/api/upload-session", { folder, minutes });
      navigate(`/transfer/${link.id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }
  async function stop(id: string) {
    await send("DELETE", `/api/upload-session/${encodeURIComponent(id)}`).catch(() => undefined);
    reload();
  }

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>UPLOAD TO VAULT</h1>
          <p className="subtitle">Phone → Vault</p>
        </div>
      </header>

      {!canUpload ? (
        <Notice>Your account can't upload. Ask a Vault admin for a folder you can write to.</Notice>
      ) : (
        <Card>
          <p className="lead">Show a QR code, scan it with your phone, and pick photos, videos or files. No app needed.</p>
          <div className="form-row">
            <label className="field">
              <span>Put files in</span>
              <select value={folder} onChange={(e) => setFolder(e.target.value)}>
                {isAdmin && <option value="">Phone Uploads (default)</option>}
                {choices
                  .filter((f) => !isAdmin || f !== "Phone Uploads")
                  .map((f) => (
                    <option key={f} value={f}>
                      {f}
                    </option>
                  ))}
              </select>
            </label>
            <label className="field">
              <span>Code works for</span>
              <select value={minutes} onChange={(e) => setMinutes(Number(e.target.value))}>
                <option value={5}>5 minutes</option>
                <option value={10}>10 minutes</option>
                <option value={30}>30 minutes</option>
                <option value={60}>1 hour</option>
              </select>
            </label>
          </div>
          {error && <Notice tone="bad">{error}</Notice>}
          <div className="setup-actions">
            <button className="btn btn-primary" onClick={create} disabled={busy || (!isAdmin && !folder && choices.length > 0 && folder === "" && false)}>
              {busy ? "Creating…" : "Show QR code"}
            </button>
          </div>
          <p className="muted small">
            Shortcut: <Kbd combo="Super + Shift + U" /> · Your phone must be on the same Wi-Fi as this computer.
          </p>
        </Card>
      )}

      <Card title="ACTIVE CODES">
        {!links ? (
          <Loading what="codes" />
        ) : links.links.length === 0 ? (
          <p className="muted">No active codes. Nothing is listening for phones right now.</p>
        ) : (
          <ul className="user-list">
            {links.links.map((l) => (
              <li key={l.id} className="user-row">
                <div className="user-head">
                  <div>
                    <strong>{l.folder}</strong>
                    <div className="muted small">
                      {l.received.length} received · until {new Date(l.expires_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} · by {l.created_by}
                    </div>
                  </div>
                  <div className="confirm-row">
                    <button className="btn btn-small" onClick={() => navigate(`/transfer/${l.id}`)}>
                      Show
                    </button>
                    <button className="btn btn-small btn-danger" onClick={() => stop(l.id)}>
                      Stop
                    </button>
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </section>
  );
}
