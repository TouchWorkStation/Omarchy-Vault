import { useEffect, useMemo, useState } from "react";
import { ApiError, send, type ShortcutEditorState, type ShortcutResult } from "../api";
import { useApi } from "../useApi";
import { Badge, Kbd, Loading, Notice } from "./ui";

interface Row {
  id: string;
  label: string;
  direction: string;
  on: boolean;
  mods: string[];
  key: string;
}

const nice = (m: string) => m[0] + m.slice(1).toLowerCase();
const combo = (mods: string[], key: string) => [...mods.map(nice), key].join(" + ");

function rowsFrom(s: ShortcutEditorState): Row[] {
  return s.checks.map((c) => {
    const cur = s.current.find((p) => p.id === c.id);
    return {
      id: c.id,
      label: c.label,
      direction: c.direction,
      // A saved setup is shown as it is; otherwise start from Vault's
      // defaults, leaving out any that are taken.
      on: cur ? true : s.current.length === 0 && c.status !== "conflict",
      mods: cur ? cur.mods : c.mods,
      key: cur ? cur.key : c.key,
    };
  });
}

function sameRows(a: Row[], b: Row[]) {
  return JSON.stringify(a.map((r) => [r.id, r.on, r.mods, r.key])) === JSON.stringify(b.map((r) => [r.id, r.on, r.mods, r.key]));
}

function Status({ r }: { r?: ShortcutResult }) {
  if (!r) return <Badge tone="muted" glyph="…">Checking</Badge>;
  switch (r.status) {
    case "available":
      return <Badge tone="good" glyph="●">Free</Badge>;
    case "conflict":
      return <Badge tone="warn" glyph="▲">Taken</Badge>;
    case "duplicate":
      return <Badge tone="warn" glyph="▲">Used twice</Badge>;
    default:
      return <Badge tone="bad" glyph="✕">Not allowed</Badge>;
  }
}

// ShortcutEditor lets you try keys for each Vault action, checks them
// against your Hyprland bindings as you go, and saves the free ones.
export function ShortcutEditor() {
  const { data, error, reload } = useApi<ShortcutEditorState>("/api/shortcuts");
  const [rows, setRows] = useState<Row[] | null>(null);
  const [saved, setSaved] = useState<Row[] | null>(null);
  const [results, setResults] = useState<Record<string, ShortcutResult>>({});
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "info" | "bad"; text: string } | null>(null);

  useEffect(() => {
    if (data && !rows) {
      const r = rowsFrom(data);
      setRows(r);
      setSaved(r);
    }
  }, [data, rows]);

  const enabled = useMemo(() => (rows ?? []).filter((r) => r.on), [rows]);

  // Check what you're trying, shortly after each change.
  useEffect(() => {
    if (!rows) return;
    const t = window.setTimeout(() => {
      send<{ results: ShortcutResult[] }>("POST", "/api/shortcuts/check", {
        bindings: enabled.map((r) => ({ id: r.id, mods: r.mods, key: r.key })),
      })
        .then((res) => setResults(Object.fromEntries(res.results.map((x) => [x.id, x]))))
        .catch(() => undefined);
    }, 200);
    return () => window.clearTimeout(t);
  }, [rows, enabled]);

  if (error && !data) return <Notice tone="bad">{error}</Notice>;
  if (!data || !rows) return <Loading what="shortcuts" />;

  const update = (id: string, change: Partial<Row>) => {
    setMsg(null);
    setRows(rows.map((r) => (r.id === id ? { ...r, ...change } : r)));
  };
  const toggleMod = (r: Row, m: string) =>
    update(r.id, { mods: r.mods.includes(m) ? r.mods.filter((x) => x !== m) : data.mod_choices.filter((x) => x === m || r.mods.includes(x)) });

  const allFree = enabled.every((r) => results[r.id]?.status === "available");
  const changed = !saved || !sameRows(rows, saved);
  const noConfig = data.sources.length === 0;

  async function save() {
    setBusy(true);
    setMsg(null);
    try {
      const next = await send<ShortcutEditorState>("POST", "/api/shortcuts", {
        bindings: enabled.map((r) => ({ id: r.id, mods: r.mods, key: r.key })),
      });
      const r = rowsFrom(next);
      setRows(r);
      setSaved(r);
      const first = next.current[0];
      setMsg({
        tone: "info",
        text:
          next.current.length === 0
            ? "Vault's shortcuts were removed."
            : `Saved. Hyprland picks them up right away. Try ${combo(first.mods, first.key)} now.`,
      });
      reload();
    } catch (e) {
      setMsg({ tone: "bad", text: e instanceof ApiError ? e.message : "Something went wrong." });
    } finally {
      setBusy(false);
    }
  }
  async function removeAll() {
    setBusy(true);
    try {
      const next = await send<ShortcutEditorState>("DELETE", "/api/shortcuts");
      const r = rowsFrom(next).map((x) => ({ ...x, on: false }));
      setRows(r);
      setSaved(r);
      setMsg({ tone: "info", text: "Vault's shortcuts were removed. Your other bindings were not touched." });
    } catch (e) {
      setMsg({ tone: "bad", text: e instanceof ApiError ? e.message : "Something went wrong." });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="sc-editor">
      <p className="muted small">
        Try different keys for each action. Vault checks them against your Hyprland bindings as you choose, and never replaces one you already use.
      </p>
      {noConfig && <Notice tone="warn">No Hyprland configuration was found on this computer, so Vault can't check for conflicts and won't add shortcuts.</Notice>}
      <ul className="sc-list">
        {rows.map((r) => {
          const res = results[r.id];
          const cur = data.current.find((p) => p.id === r.id);
          return (
            <li key={r.id} className={`sc-row${r.on ? "" : " off"}`}>
              <div className="sc-head">
                <label className="sc-on">
                  <input type="checkbox" checked={r.on} onChange={(e) => update(r.id, { on: e.target.checked })} />
                  <span>
                    <strong>{r.label}</strong>
                    {r.direction && <span className="muted small"> · {r.direction}</span>}
                  </span>
                </label>
                <span className="muted small">{cur ? <>Now: <Kbd combo={combo(cur.mods, cur.key)} /></> : "Not set up"}</span>
              </div>
              {r.on && (
                <>
                  <div className="sc-keys">
                    {data.mod_choices.map((m) => (
                      <button
                        key={m}
                        className={`chip${r.mods.includes(m) ? " on" : ""}`}
                        aria-pressed={r.mods.includes(m)}
                        onClick={() => toggleMod(r, m)}
                      >
                        {nice(m)}
                      </button>
                    ))}
                    <span className="muted">+</span>
                    <select aria-label={`Key for ${r.label}`} value={r.key} onChange={(e) => update(r.id, { key: e.target.value })}>
                      {data.key_choices.map((k) => (
                        <option key={k} value={k}>
                          {k}
                        </option>
                      ))}
                    </select>
                    <Status r={res} />
                  </div>
                  {res?.status === "conflict" && (
                    <p className="sc-note">
                      {res.combo} is already used by{" "}
                      <strong>{res.conflicts?.map((b) => b.description || `${b.dispatcher} ${b.arg ?? ""}`.trim()).join(", ")}</strong>.
                      {res.suggestion && (
                        <>
                          {" "}
                          <button className="btn btn-small" onClick={() => update(r.id, { mods: res.suggestion!.mods, key: res.suggestion!.key })}>
                            Use {combo(res.suggestion.mods, res.suggestion.key)}
                          </button>
                        </>
                      )}
                    </p>
                  )}
                  {(res?.status === "duplicate" || res?.status === "invalid") && <p className="sc-note">{res.message}</p>}
                </>
              )}
            </li>
          );
        })}
      </ul>
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
      <div className="setup-actions">
        <button className="btn btn-primary" onClick={save} disabled={busy || noConfig || !changed || !allFree}>
          {busy ? "Saving…" : enabled.length === 0 ? "Save (no shortcuts)" : "Save shortcuts"}
        </button>
        {data.current.length > 0 && (
          <button className="btn btn-danger" onClick={removeAll} disabled={busy}>
            Remove Vault's shortcuts
          </button>
        )}
      </div>
      <p className="muted small">
        Shortcuts with Super can't be tried inside this page (Hyprland catches them first): save, then press them. Saved to <code>{data.file}</code>.
      </p>
    </div>
  );
}
