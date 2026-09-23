import { useState } from "react";
import { ApiError, send, UNLIMITED, type BrowseEntry, type BrowseResponse, type LinkView, type Session } from "../api";
import { navigate } from "../App";
import { Card, Kbd, Loading, Notice } from "../components/ui";
import { bytes } from "../format";
import { useApi } from "../useApi";

function errText(e: unknown) {
  return e instanceof ApiError ? e.message : "Something went wrong.";
}

function until(iso: string) {
  const d = new Date(iso);
  const sameDay = d.toDateString() === new Date().toDateString();
  return sameDay
    ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleString([], { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

// Picker: browse the Vault folder by folder and choose one file or folder.
function Picker({ onPick, picked }: { onPick: (e: BrowseEntry | null) => void; picked: BrowseEntry | null }) {
  const [path, setPath] = useState("");
  const { data, error } = useApi<BrowseResponse>(`/api/browse?path=${encodeURIComponent(path)}`);
  const parts = path ? path.split("/") : [];

  function open(p: string) {
    setPath(p);
    onPick(null);
  }

  return (
    <div className="picker">
      <nav className="crumbs" aria-label="Folder">
        <button className="crumb" onClick={() => open("")}>
          Vault
        </button>
        {parts.map((name, i) => (
          <span key={i}>
            <span className="muted"> › </span>
            <button className="crumb" onClick={() => open(parts.slice(0, i + 1).join("/"))}>
              {name}
            </button>
          </span>
        ))}
      </nav>
      {error ? (
        <Notice tone="bad">{error}</Notice>
      ) : !data ? (
        <Loading what="folders" />
      ) : data.entries.length === 0 ? (
        <p className="muted">{path ? "This folder is empty." : "No folders you can open yet."}</p>
      ) : (
        <ul className="pick-list">
          {path && (
            <li className="pick-item">
              <button className="pick-row" onClick={() => open(parts.slice(0, -1).join("/"))}>
                <span className="pick-icon">↑</span>
                <span className="pick-name">Up</span>
              </button>
            </li>
          )}
          {data.entries.map((e) => (
            <li key={e.path} className="pick-item">
              <button
                className={`pick-row${picked?.path === e.path ? " picked" : ""}`}
                aria-pressed={picked?.path === e.path}
                onClick={() => onPick(e)}
                onDoubleClick={() => e.dir && open(e.path)}
              >
                <span className="pick-icon">{e.dir ? "▣" : "▤"}</span>
                <span className="pick-name">{e.name}</span>
                <span className="muted small">{e.dir ? "folder" : bytes(e.size)}</span>
              </button>
              {e.dir && (
                <button className="btn btn-small" onClick={() => open(e.path)} aria-label={`Open ${e.name}`}>
                  Open ›
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {data?.truncated && <p className="muted small">Showing the first 5000 items.</p>}
    </div>
  );
}

function SendPanel({ item, canShare }: { item: BrowseEntry; canShare: boolean }) {
  const [mode, setMode] = useState<"phone" | "share">("phone");
  const [minutes, setMinutes] = useState(10);
  const [count, setCount] = useState(1);
  const [shareMinutes, setShareMinutes] = useState(60);
  const [shareCount, setShareCount] = useState(0);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function create() {
    setBusy(true);
    setError(null);
    try {
      const link =
        mode === "phone"
          ? await send<LinkView>("POST", "/api/download-session", { path: item.path, minutes, max_downloads: count })
          : await send<LinkView>("POST", "/api/share", {
              path: item.path,
              minutes: shareMinutes,
              max_downloads: shareCount,
              password,
            });
      navigate(`/transfer/${link.id}`);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={item.dir ? "SEND THIS FOLDER" : "SEND THIS FILE"}>
      <p className="lead">
        <strong>{item.name}</strong> {item.dir ? <span className="muted">· folder, downloads as .zip</span> : <span className="muted">· {bytes(item.size)}</span>}
      </p>
      {canShare && (
        <div className="segmented" role="tablist">
          <button role="tab" aria-selected={mode === "phone"} className={mode === "phone" ? "on" : ""} onClick={() => setMode("phone")}>
            To my phone
          </button>
          <button role="tab" aria-selected={mode === "share"} className={mode === "share" ? "on" : ""} onClick={() => setMode("share")}>
            Share link
          </button>
        </div>
      )}
      {mode === "phone" ? (
        <div className="form-row">
          <label className="field">
            <span>Code works for</span>
            <select value={minutes} onChange={(e) => setMinutes(Number(e.target.value))}>
              <option value={5}>5 minutes</option>
              <option value={10}>10 minutes</option>
              <option value={30}>30 minutes</option>
              <option value={60}>1 hour</option>
            </select>
          </label>
          <label className="field">
            <span>Downloads</span>
            <select value={count} onChange={(e) => setCount(Number(e.target.value))}>
              <option value={1}>Once</option>
              <option value={2}>2 phones</option>
              <option value={5}>5 phones</option>
              <option value={10}>10 phones</option>
            </select>
          </label>
        </div>
      ) : (
        <>
          <div className="form-row">
            <label className="field">
              <span>Link works for</span>
              <select value={shareMinutes} onChange={(e) => setShareMinutes(Number(e.target.value))}>
                <option value={10}>10 minutes</option>
                <option value={60}>1 hour</option>
                <option value={4 * 60}>4 hours</option>
                <option value={24 * 60}>24 hours</option>
              </select>
            </label>
            <label className="field">
              <span>Downloads</span>
              <select value={shareCount} onChange={(e) => setShareCount(Number(e.target.value))}>
                <option value={1}>One</option>
                <option value={5}>Up to 5</option>
                <option value={25}>Up to 25</option>
                <option value={0}>Unlimited until it expires</option>
              </select>
            </label>
          </div>
          <label className="field">
            <span>Password (optional)</span>
            <input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Leave empty for no password" />
          </label>
          <p className="muted small">Read only: people with the link can download, never change or delete. Works on this Wi-Fi only, while Vault is on.</p>
        </>
      )}
      {error && <Notice tone="bad">{error}</Notice>}
      <div className="setup-actions">
        <button className="btn btn-primary" onClick={create} disabled={busy}>
          {busy ? "Creating…" : mode === "phone" ? "Show QR code" : "Create share link"}
        </button>
      </div>
    </Card>
  );
}

function ActiveLinks({ kind, title, empty }: { kind: "download" | "share"; title: string; empty: string }) {
  const { data, reload } = useApi<{ links: LinkView[] }>(kind === "share" ? "/api/shares" : "/api/download-sessions", 10_000);
  const [copied, setCopied] = useState<string | null>(null);

  async function stop(id: string) {
    await send("DELETE", `/api/link/${encodeURIComponent(id)}`).catch(() => undefined);
    reload();
  }
  async function copy(l: LinkView) {
    if (!l.url) return;
    try {
      await navigator.clipboard.writeText(l.url);
      setCopied(l.id);
      window.setTimeout(() => setCopied(null), 1500);
    } catch {
      /* clipboard unavailable: the link is shown on its page */
    }
  }

  return (
    <Card title={title}>
      {!data ? (
        <Loading what="links" />
      ) : data.links.length === 0 ? (
        <p className="muted">{empty}</p>
      ) : (
        <ul className="user-list">
          {data.links.map((l) => (
            <li key={l.id} className="user-row">
              <div className="user-head">
                <div>
                  <strong>{l.path}</strong>
                  <div className="muted small">
                    {l.files} downloaded{l.max_files < UNLIMITED ? ` of ${l.max_files}` : ""} · until {until(l.expires_at)}
                    {l.has_password && " · password"} · by {l.created_by}
                  </div>
                </div>
                <div className="confirm-row">
                  {kind === "share" && l.url && (
                    <button className="btn btn-small" onClick={() => copy(l)}>
                      {copied === l.id ? "Copied" : "Copy link"}
                    </button>
                  )}
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
  );
}

// The page Super+Alt+D opens.
export function Download() {
  const { data: session } = useApi<Session>("/api/session");
  const [picked, setPicked] = useState<BrowseEntry | null>(null);
  if (!session) return <Loading what="Download" />;
  const canShare = session.can_change || session.user?.role === "family";
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>DOWNLOAD FROM VAULT</h1>
          <p className="subtitle">Vault → Phone</p>
        </div>
      </header>

      <Card title="CHOOSE A FILE OR FOLDER">
        <Picker onPick={setPicked} picked={picked} />
        <p className="muted small">
          Shortcut: <Kbd combo="Super + Alt + D" /> · Click to choose, <em>Open</em> to go into a folder.
        </p>
      </Card>

      {picked && <SendPanel key={picked.path} item={picked} canShare={canShare} />}

      <ActiveLinks kind="download" title="ACTIVE DOWNLOAD CODES" empty="No active download codes." />
      {canShare && <ActiveLinks kind="share" title="SHARE LINKS" empty="No share links. Nothing is shared right now." />}
    </section>
  );
}
