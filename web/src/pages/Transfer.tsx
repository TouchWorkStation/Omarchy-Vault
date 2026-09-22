import { useEffect, useState } from "react";
import { ApiError, send, type LinkView } from "../api";
import { Link } from "../App";
import { Loading, Notice } from "../components/ui";
import { bytes } from "../format";
import { useApi } from "../useApi";

function countdown(until: string, now: number) {
  const left = Math.max(0, Math.round((new Date(until).getTime() - now) / 1000));
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
}

// QrImage renders the server-made SVG as an image (no inline markup).
export function QrImage({ svg, size = 300 }: { svg: string; size?: number }) {
  return (
    <img
      className="qr qr-big"
      width={size}
      height={size}
      alt="QR code: scan it with your phone's camera"
      src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`}
    />
  );
}

// The window Super+Shift+U opens: a big QR code and live progress.
export function Transfer({ id }: { id: string }) {
  const { data: link, error, reload } = useApi<LinkView>(`/api/upload-session/${encodeURIComponent(id)}`, 2000);
  const [now, setNow] = useState(Date.now());
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);

  if (error && !link) return <Notice tone="bad">{error}</Notice>;
  if (!link) return <Loading what="the upload code" />;

  async function stop() {
    try {
      await send("DELETE", `/api/upload-session/${encodeURIComponent(id)}`);
      reload();
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Something went wrong.");
    }
  }
  async function again() {
    try {
      const n = await send<LinkView>("POST", "/api/upload-session", { folder: link!.folder });
      window.location.assign(`/transfer/${n.id}`);
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Something went wrong.");
    }
  }

  const active = link.state === "active";
  const total = link.received.reduce((n, f) => n + f.size, 0);
  return (
    <section className="page transfer">
      <header className="transfer-head">
        <h1>UPLOAD TO VAULT</h1>
        <p className="subtitle">Phone → Vault · into {link.folder}</p>
      </header>

      {active && link.qr_svg ? (
        <div className="transfer-qr">
          <QrImage svg={link.qr_svg} />
          <p className="lead">Scan with your phone's camera</p>
          <p className="muted small">Your phone must be on the same Wi-Fi as this computer.</p>
          <p className="countdown">
            Valid for <strong>{countdown(link.expires_at, now)}</strong>
          </p>
        </div>
      ) : active ? (
        <Notice>This code is active, but it was created before Vault restarted, so it can't be shown again. Create a new one.</Notice>
      ) : (
        <Notice tone="warn">
          This code has {link.state === "stopped" ? "been stopped" : link.state === "full" ? "reached its limit" : "expired"}. Phones can no longer use it.
        </Notice>
      )}

      <div className="card">
        <h2>
          RECEIVED · {link.received.length} {link.received.length === 1 ? "FILE" : "FILES"} {total > 0 && `· ${bytes(total)}`}
        </h2>
        {link.received.length === 0 ? (
          <p className="muted">Nothing yet. Files appear here as they arrive.</p>
        ) : (
          <ul className="received">
            {link.received
              .slice()
              .reverse()
              .map((f, i) => (
                <li key={i}>
                  <span className="ok-glyph">✓</span> <span className="received-name">{f.name}</span>
                  <span className="muted small">{bytes(f.size)}</span>
                </li>
              ))}
          </ul>
        )}
      </div>

      {msg && <Notice tone="bad">{msg}</Notice>}
      <div className="setup-actions center">
        {active ? (
          <button className="btn btn-danger" onClick={stop}>
            Stop
          </button>
        ) : (
          <button className="btn btn-primary" onClick={again}>
            New code
          </button>
        )}
        <Link to="/upload" className="btn">
          Done
        </Link>
      </div>
    </section>
  );
}
