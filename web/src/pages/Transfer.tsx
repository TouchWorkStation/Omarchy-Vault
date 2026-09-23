import { useEffect, useState } from "react";
import { ApiError, send, UNLIMITED, type LinkView } from "../api";
import { Link } from "../App";
import { Loading, Notice } from "../components/ui";
import { bytes } from "../format";
import { useApi } from "../useApi";

function countdown(until: string, now: number) {
  const left = Math.max(0, Math.round((new Date(until).getTime() - now) / 1000));
  const h = Math.floor(left / 3600);
  if (h >= 48) return `${Math.floor(h / 24)} days`;
  if (h > 0) return `${h} h ${Math.floor((left % 3600) / 60)} min`;
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
}

const titles = {
  upload: { title: "UPLOAD TO VAULT", dir: "Phone → Vault", back: "/upload" },
  download: { title: "DOWNLOAD FROM VAULT", dir: "Vault → Phone", back: "/download" },
  share: { title: "SHARE LINK", dir: "Read only", back: "/download" },
};

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

// The window Super+Alt+U / Super+Alt+D open: a big QR code and live
// progress. Share links show the same page with the link to copy.
export function Transfer({ id }: { id: string }) {
  const { data: link, error, reload } = useApi<LinkView>(`/api/link/${encodeURIComponent(id)}`, 2000);
  const [now, setNow] = useState(Date.now());
  const [msg, setMsg] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);

  if (error && !link) return <Notice tone="bad">{error}</Notice>;
  if (!link) return <Loading what="the code" />;
  const t = titles[link.kind] ?? titles.upload;
  const up = link.kind === "upload";
  // Files sent from anywhere on this computer (copied in the file manager).
  const local = !up && (link.path ?? "").startsWith("/");
  const what = local
    ? (link.path ?? "").split("\n").map((p) => p.split("/").pop()).join(", ")
    : link.path;

  async function stop() {
    try {
      await send("DELETE", `/api/link/${encodeURIComponent(id)}`);
      reload();
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Something went wrong.");
    }
  }
  async function copy() {
    if (!link?.url) return;
    try {
      await navigator.clipboard.writeText(link.url);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* the link stays visible to copy by hand */
    }
  }
  async function again() {
    try {
      const n = up
        ? await send<LinkView>("POST", "/api/upload-session", { folder: link!.folder })
        : await send<LinkView>("POST", "/api/download-session", { path: link!.path, max_downloads: link!.max_files });
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
        <h1>{t.title}</h1>
        <p className="subtitle">
          {t.dir} · {up ? `into ${link.folder}` : what}
          {link.has_password && " · password protected"}
        </p>
      </header>

      {active && link.qr_svg ? (
        <div className="transfer-qr">
          <QrImage svg={link.qr_svg} />
          <p className="lead">Scan with your phone's camera</p>
          <p className="muted small">
            {link.kind === "share"
              ? "Works for phones and computers on this network while Vault is on."
              : "Your phone must be on the same Wi-Fi as this computer."}
          </p>
          {link.kind === "share" && link.url && (
            <div className="share-url">
              <input readOnly value={link.url} onFocus={(e) => e.currentTarget.select()} aria-label="Share link" />
              <button className="btn btn-small" onClick={copy}>
                {copied ? "Copied" : "Copy"}
              </button>
            </div>
          )}
          <p className="countdown">
            Valid for <strong>{countdown(link.expires_at, now)}</strong>
          </p>
        </div>
      ) : active ? (
        <Notice>This code is active, but it was created before Vault restarted, so it can't be shown again. Create a new one.</Notice>
      ) : (
        <Notice tone="warn">
          {link.state === "full" && !up
            ? "Done: downloaded. This code can't be used again."
            : `This ${link.kind === "share" ? "link" : "code"} has ${link.state === "stopped" ? "been stopped" : link.state === "full" ? "reached its limit" : "expired"}. Phones can no longer use it.`}
        </Notice>
      )}

      <div className="card">
        <h2>
          {up ? "RECEIVED" : "DOWNLOADED"} · {link.received.length} {link.received.length === 1 ? "FILE" : "FILES"} {total > 0 && `· ${bytes(total)}`}
          {!up && link.max_files < UNLIMITED && ` · ${Math.max(0, link.max_files - link.files)} LEFT`}
        </h2>
        {link.received.length === 0 ? (
          <p className="muted">{up ? "Nothing yet. Files appear here as they arrive." : "Nothing downloaded yet."}</p>
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
        ) : link.kind !== "share" && !local ? (
          <button className="btn btn-primary" onClick={again}>
            New code
          </button>
        ) : null}
        <Link to={t.back} className="btn">
          Done
        </Link>
      </div>
    </section>
  );
}
