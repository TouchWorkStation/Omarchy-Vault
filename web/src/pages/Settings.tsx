import type { Service, SettingsResponse, Status } from "../api";
import { Badge, Card, Loading, Notice } from "../components/ui";
import { ShortcutEditor } from "../components/ShortcutEditor";
import { useApi } from "../useApi";

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

export function Settings() {
  const { data, error } = useApi<SettingsResponse>("/api/settings");
  const { data: status } = useApi<Status>("/api/status");

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>SETTINGS</h1>
          <p className="subtitle">Shortcuts and how Vault is set up</p>
        </div>
      </header>
      {error && <Notice tone="bad">{error}</Notice>}
      {data?.config_error && <Notice tone="bad">Settings file problem: {data.config_error}</Notice>}

      <Card title="KEYBOARD SHORTCUTS">
        <ShortcutEditor />
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
