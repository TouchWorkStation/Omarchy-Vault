import type { Service, SettingsResponse, ShortcutReport, Status } from "../api";
import { Badge, Card, Kbd, Loading, Notice } from "../components/ui";
import { useApi } from "../useApi";
import { useState } from "react";

function ServiceRow({ s, current }: { s: Service; current: number }) {
  let badge;
  if (s.installed && (!s.unit || s.unit_state === "active")) badge = <Badge tone="good" glyph="●">Ready</Badge>;
  else if (s.installed) badge = <Badge tone="muted" glyph="○">{s.unit_state ?? "Installed"}</Badge>;
  else if (s.required) badge = <Badge tone="bad" glyph="✕">Missing</Badge>;
  else badge = <Badge tone="muted" glyph="–">Not installed</Badge>;
  return (
    <li className="service">
      <div>
        <div>{s.name}</div>
        <div className="muted small">
          {s.purpose}
          {!s.installed && s.milestone > current ? ` · needed from Milestone ${s.milestone}` : ""}
        </div>
      </div>
      {badge}
    </li>
  );
}

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      className="btn btn-small"
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          window.setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? "Copied" : "Copy Binding Command"}
    </button>
  );
}

function Shortcuts() {
  const { data } = useApi<ShortcutReport>("/api/shortcuts");
  if (!data) return <Loading what="shortcuts" />;
  return (
    <>
      <ul className="shortcut-detail">
        {data.checks.map((c) => (
          <li key={c.id}>
            <div className="shortcut-row">
              <div>
                <div>{c.label}</div>
                {c.direction && <div className="muted small">{c.direction}</div>}
              </div>
              <Kbd combo={c.combo} />
            </div>
            {c.status === "conflict" && (
              <div className="conflict">
                <p>
                  <strong>Shortcut Conflict</strong> — {c.combo} is already assigned to{" "}
                  {c.conflicts?.map((b) => b.description || `${b.dispatcher} ${b.arg ?? ""}`).join(", ")}.
                </p>
                <p className="muted small">
                  Options: Choose Another Shortcut
                  {c.suggestion ? ` (for example ${[...c.suggestion.mods, c.suggestion.key].map((x) => x[0] + x.slice(1).toLowerCase()).join(" + ")})` : ""} ·
                  Copy Binding Command · Skip Shortcut
                </p>
                <CopyButton text={c.binding_line} />
              </div>
            )}
            {c.status === "available" && <p className="muted small">Free. Vault can add it when shortcuts are installed (Milestone 4).</p>}
            {c.status === "installed" && <p className="muted small">Installed and working.</p>}
            {c.status === "unknown" && <p className="muted small">Could not check this computer's Hyprland config.</p>}
          </li>
        ))}
      </ul>
      {data.warnings?.map((w) => (
        <p key={w} className="muted small">
          ! {w}
        </p>
      ))}
    </>
  );
}

export function Settings() {
  const { data, error } = useApi<SettingsResponse>("/api/settings");
  const { data: status } = useApi<Status>("/api/status");

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>SETTINGS</h1>
          <p className="subtitle">Read-only in this version</p>
        </div>
      </header>
      {error && <Notice tone="bad">{error}</Notice>}
      {data?.config_error && <Notice tone="bad">Settings file problem: {data.config_error}</Notice>}

      <Card title="SHORTCUTS">
        <Shortcuts />
      </Card>

      <div className="two-col">
        <Card title="VAULT">
          {!data ? (
            <Loading what="settings" />
          ) : (
            <dl className="kv">
              <dt>Vault</dt>
              <dd>{data.config.vault_root}</dd>
              <dt>Stored on</dt>
              <dd>{status?.storage.sources[0]?.data_dir ?? "not set up"}</dd>
              <dt>Phone uploads go to</dt>
              <dd>{data.config.preferences.upload_folder}</dd>
              <dt>Upload links expire</dt>
              <dd>{data.config.preferences.upload_expiry_minutes} min</dd>
              <dt>Download links expire</dt>
              <dd>
                {data.config.preferences.download_expiry_minutes} min · {data.config.preferences.download_max_count} download
              </dd>
              <dt>Dashboard address</dt>
              <dd>http://{data.config.listen}</dd>
              <dt>Settings file</dt>
              <dd>{data.config_found ? "~/.config/omarchy-vault/config.json" : "not created yet"}</dd>
            </dl>
          )}
        </Card>
        <Card title="SERVICES">
          {!status ? (
            <Loading what="services" />
          ) : (
            <ul className="services">
              {status.services.map((s) => (
                <ServiceRow key={s.id} s={s} current={status.milestone} />
              ))}
            </ul>
          )}
        </Card>
      </div>
    </section>
  );
}
