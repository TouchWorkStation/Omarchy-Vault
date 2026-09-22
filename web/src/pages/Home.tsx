import type { ShortcutBrief, Status } from "../api";
import { Link } from "../App";
import { icons } from "../components/Icons";
import { Badge, Card, Kbd, Loading, Meter, Notice, Stat } from "../components/ui";
import { bytes } from "../format";
import { useApi } from "../useApi";

function driveTile(s: Status) {
  const d = s.drives;
  if (!d) return <Stat label="Drives" value="—" sub={s.drives_error ?? "Unavailable"} />;
  let value: string;
  if (d.critical > 0) value = `${d.critical} Critical`;
  else if (d.warning > 0) value = `${d.warning} Warning`;
  else if (d.healthy > 0) value = `${d.healthy} Healthy`;
  else value = `${d.total} Found`;
  return <Stat label="Drives" value={value} sub={`${d.system} protected · ${d.available} available`} />;
}

function shortcutBadge(s: ShortcutBrief["status"]) {
  switch (s) {
    case "installed":
      return <Badge tone="good" glyph="●">Active</Badge>;
    case "available":
      return <Badge tone="muted" glyph="○">Free</Badge>;
    case "conflict":
      return <Badge tone="warn" glyph="▲">Conflict</Badge>;
    default:
      return <Badge tone="muted" glyph="?">Unchecked</Badge>;
  }
}

const actions = [
  { to: "/upload", icon: icons.upload, label: "Upload", sub: "Phone → Vault", m: 4 },
  { to: "/download", icon: icons.download, label: "Download", sub: "Vault → Phone", m: 5 },
  { to: "/files", icon: icons.files, label: "Open Files", sub: "Browse your Vault", m: 3 },
];

export function Home() {
  const { data: s, error } = useApi<Status>("/api/status", 60_000);

  if (error && !s) {
    return (
      <section className="page">
        <h1>VAULT</h1>
        <Notice tone="bad">
          {error} Start it with <code>systemctl --user start omarchy-vault</code>, then reload.
        </Notice>
      </section>
    );
  }
  if (!s) return <Loading what="Vault" />;

  const st = s.storage;
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>VAULT</h1>
          <p className="subtitle">
            {s.hostname} · v{s.version}
          </p>
        </div>
      </header>

      {s.warnings?.map((w) => (
        <Notice key={w} tone="warn">
          {w}
        </Notice>
      ))}

      {!s.setup_complete && (
        <Notice>
          Vault storage isn't set up yet.{" "}
          {s.drives && s.drives.available > 0 ? (
            <>
              {s.drives.available} drive{s.drives.available === 1 ? " is" : "s are"} ready to use.{" "}
            </>
          ) : null}
          <Link to="/storage">See your drives</Link>. Choosing storage arrives in Milestone 2.
        </Notice>
      )}

      <div className="tiles">
        <div className="tile">
          {st.configured ? (
            <>
              <Stat
                label="Storage"
                value={`${bytes(st.used_bytes)} / ${bytes(st.total_bytes)}`}
                sub={`${bytes(st.free_bytes)} available`}
              />
              <Meter used={st.used_bytes} total={st.total_bytes} label="Vault storage used" />
            </>
          ) : (
            <Stat label="Storage" value="Not set up" sub={st.root} />
          )}
        </div>
        <div className="tile">{driveTile(s)}</div>
        <div className="tile">
          <Stat
            label="Remote Access"
            value={s.remote.state === "not_configured" ? "Off" : s.remote.state}
            sub={s.remote.domain ?? "Local only"}
          />
        </div>
        <div className="tile">
          <Stat label="Users" value={s.users.count ?? "—"} sub={`Milestone ${s.users.milestone}`} />
        </div>
      </div>

      <Card title="QUICK ACTIONS">
        <div className="actions">
          {actions.map((a) => (
            <Link key={a.to} to={a.to} className="action">
              {a.icon}
              <span className="action-label">{a.label}</span>
              <span className="action-sub">{a.sub}</span>
              <span className="action-soon">Milestone {a.m}</span>
            </Link>
          ))}
        </div>
      </Card>

      <div className="two-col">
        <Card title="SHORTCUTS" actions={<Link to="/settings" className="card-link">Details</Link>}>
          <ul className="shortcut-list">
            {s.shortcuts.map((k) => (
              <li key={k.id}>
                <span className="shortcut-label">{k.label}</span>
                <Kbd combo={k.combo} />
                {shortcutBadge(k.status)}
              </li>
            ))}
          </ul>
          <p className="muted small">Vault never overwrites an existing shortcut.</p>
        </Card>

        <Card title="RECENT FILES">
          <p className="muted">Recent uploads and changes appear here once Files arrive in Milestone 3.</p>
        </Card>
      </div>
    </section>
  );
}
