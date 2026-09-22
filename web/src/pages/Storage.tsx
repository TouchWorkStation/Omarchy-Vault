import { ApiError, send, type Disk, type DiskStatus, type Inventory, type PoolResponse, type Session, type StorageStatus, type Volume } from "../api";
import { Link } from "../App";
import { icons } from "../components/Icons";
import { Badge, Card, HealthBadge, Loading, Meter, Notice } from "../components/ui";
import { bytes, hours } from "../format";
import { useApi } from "../useApi";
import { useState, type ReactElement } from "react";

const statusBadge: Record<DiskStatus, ReactElement> = {
  system: (
    <Badge tone="protect" glyph="◆">
      SYSTEM · PROTECTED
    </Badge>
  ),
  available: (
    <Badge tone="accent" glyph="●">
      AVAILABLE
    </Badge>
  ),
  unmounted: (
    <Badge tone="muted" glyph="○">
      NOT MOUNTED
    </Badge>
  ),
  unsupported: (
    <Badge tone="muted" glyph="–">
      UNSUPPORTED
    </Badge>
  ),
  "read-only": (
    <Badge tone="muted" glyph="–">
      READ-ONLY
    </Badge>
  ),
};

const kindLabel: Record<string, string> = {
  nvme: "NVMe",
  ssd: "SSD",
  hdd: "Hard drive",
  usb: "USB",
  sd: "SD card",
  virtual: "Virtual",
};

function VolumeRow({ v }: { v: Volume }) {
  const mounted = v.mountpoints.length > 0;
  const size = v.fs_size_bytes ?? 0;
  const used = v.fs_used_bytes ?? 0;
  return (
    <li className="volume">
      <div className="volume-head">
        <span className="mono">{v.name}</span>
        <span className="muted">{v.fstype || "no filesystem"}</span>
        <span className="muted volume-mount">{mounted ? v.mountpoints.join(", ") : "not mounted"}</span>
        {v.adoptable && (
          <Link to={`/setup?volume=${encodeURIComponent(v.name)}`} className="btn btn-small">
            Use this drive
          </Link>
        )}
      </div>
      {size > 0 && (
        <div className="volume-usage">
          <Meter used={used} total={size} label={`${v.name} used`} />
          <span className="muted small">
            {bytes(v.fs_avail_bytes)} free of {bytes(size)}
          </span>
        </div>
      )}
      {v.notes?.map((n) => (
        <p key={n} className="muted small">
          {n}
        </p>
      ))}
    </li>
  );
}

function DiskCard({ d }: { d: Disk }) {
  const h = d.health;
  return (
    <Card className={`disk ${d.system ? "disk-system" : ""}`}>
      <div className="disk-head">
        <div className="disk-title">
          {icons.storage}
          <div>
            <h3>{d.display_name}</h3>
            <p className="muted small">
              {bytes(d.size_bytes)} · {kindLabel[d.kind] ?? d.kind} · {d.path}
              {d.removable ? " · removable" : ""}
            </p>
          </div>
        </div>
        <div className="disk-badges">
          {statusBadge[d.status]}
          <HealthBadge status={h.status} />
        </div>
      </div>

      {d.protected_reason && (
        <p className="protect-note">
          {icons.shield} {d.protected_reason}
        </p>
      )}

      {(h.temperature_c != null || h.power_on_hours != null || h.reallocated_sectors != null) && (
        <dl className="health-facts">
          {h.temperature_c != null && (
            <div>
              <dt>Temperature</dt>
              <dd>{h.temperature_c}°C</dd>
            </div>
          )}
          {h.power_on_hours != null && (
            <div>
              <dt>Powered on</dt>
              <dd>{hours(h.power_on_hours)}</dd>
            </div>
          )}
          {h.smart_passed != null && (
            <div>
              <dt>SMART</dt>
              <dd>{h.smart_passed ? "Passed" : "FAILED"}</dd>
            </div>
          )}
          {h.reallocated_sectors != null && (
            <div>
              <dt>Reallocated</dt>
              <dd>{h.reallocated_sectors}</dd>
            </div>
          )}
        </dl>
      )}
      {h.reasons?.map((r) => (
        <Notice key={r} tone={h.status === "critical" ? "bad" : "warn"}>
          {r}
        </Notice>
      ))}
      {h.status === "unknown" && h.message && <p className="muted small">Health: {h.message}</p>}

      {d.volumes.length > 0 ? (
        <ul className="volumes">
          {d.volumes.map((v) => (
            <VolumeRow key={v.path + v.name} v={v} />
          ))}
        </ul>
      ) : (
        <p className="muted small">No partitions or filesystems. Vault never formats drives.</p>
      )}
      {d.notes?.map((n) => (
        <p key={n} className="muted small">
          {n}
        </p>
      ))}
    </Card>
  );
}

const stateLabel: Record<string, ReactElement> = {
  ready: <Badge tone="good" glyph="●">Ready</Badge>,
  drive_missing: <Badge tone="bad" glyph="✕">Offline</Badge>,
  drive_moved: <Badge tone="warn" glyph="▲">Drive moved</Badge>,
  problem: <Badge tone="bad" glyph="✕">Problem</Badge>,
};

function VaultStorageCard({ onChanged }: { onChanged: () => void }) {
  const { data: st, reload } = useApi<StorageStatus>("/api/storage");
  const { data: session } = useApi<Session>("/api/session");
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "info" | "bad"; text: string } | null>(null);
  if (!st) return null;

  if (!st.configured) {
    return (
      <>
        {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
        <Card className="setup-cta">
          <div>
            <h2>VAULT STORAGE</h2>
            <p className="lead">Not set up yet. Choose a drive below or run the setup.</p>
          </div>
          <Link to="/setup" className="btn btn-primary">
            Set up Vault
          </Link>
        </Card>
      </>
    );
  }

  const src = st.sources[0];
  async function forget() {
    setBusy(true);
    try {
      const r = await send<PoolResponse>("DELETE", "/api/pool");
      setMsg({ tone: "info", text: r.notes?.join(" ") ?? "Vault no longer uses this drive." });
      reload();
      onChanged();
    } catch (e) {
      setMsg({ tone: "bad", text: e instanceof ApiError ? e.message : "Something went wrong." });
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  }

  return (
    <Card title="VAULT STORAGE" actions={stateLabel[st.state]}>
      <div className="vault-storage">
        <div>
          <div className="stat-value">{src.label}</div>
          <p className="muted small mono">{src.data_dir}</p>
        </div>
        {st.state === "ready" && (
          <div className="vault-usage">
            <Meter used={st.used_bytes} total={st.total_bytes} label="Vault storage used" />
            <span className="muted small">
              {bytes(st.used_bytes)} used · {bytes(st.free_bytes)} available · {bytes(st.total_bytes)} total
            </span>
          </div>
        )}
      </div>
      {st.state !== "ready" && st.message && <Notice tone="warn">{st.message}</Notice>}
      {st.state === "drive_moved" && src.volume && (
        <p>
          <Link to={`/setup?volume=${encodeURIComponent(src.volume)}`} className="btn btn-small">
            Use it at its new location
          </Link>
        </p>
      )}
      <p className="muted small">
        {st.root_link.state === "ok" ? (
          <>
            Also at <code>{st.root}</code>
          </>
        ) : st.root_link.state === "missing" ? (
          <>
            Optional: run <code>vaultctl link</code> to also reach it at <code>{st.root}</code>.
          </>
        ) : (
          <>
            <code>{st.root}</code> already exists and is not Vault's; Vault leaves it alone.
          </>
        )}
      </p>
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
      {session?.can_change &&
        (confirming ? (
          <div className="confirm-row">
            <span>Stop using this drive? Every file stays where it is.</span>
            <button className="btn btn-small" onClick={() => setConfirming(false)} disabled={busy}>
              Cancel
            </button>
            <button className="btn btn-small btn-danger" onClick={forget} disabled={busy}>
              Stop using
            </button>
          </div>
        ) : (
          <div className="confirm-row">
            <Link to="/setup" className="btn btn-small">
              Change drive
            </Link>
            <button className="btn btn-small" onClick={() => setConfirming(true)}>
              Stop using this drive
            </button>
          </div>
        ))}
    </Card>
  );
}

export function Storage() {
  const [refresh, setRefresh] = useState(0);
  const { data, error, loading } = useApi<Inventory>(`/api/disks${refresh ? `?refresh=1&n=${refresh}` : ""}`);

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>STORAGE</h1>
          <p className="subtitle">Drives attached to this computer</p>
        </div>
        <button className="btn" onClick={() => setRefresh((n) => n + 1)} disabled={loading}>
          {icons.refresh} Rescan
        </button>
      </header>

      {error && <Notice tone="bad">{error}</Notice>}
      {!data && !error && <Loading what="drives" />}

      {data && (
        <>
          {data.warnings?.map((w) => (
            <Notice key={w} tone="warn">
              {w}
            </Notice>
          ))}
          <VaultStorageCard onChanged={() => setRefresh((n) => n + 1)} />
          <p className="summary-line">
            {data.summary.total} drives · {data.summary.system} protected · {data.summary.available} available ·{" "}
            {data.summary.unmounted} not mounted · {data.summary.unsupported} unsupported
          </p>
          <div className="disk-list">
            {data.disks.map((d) => (
              <DiskCard key={d.id} d={d} />
            ))}
          </div>
          <Notice>
            Vault only uses drives that are already mounted. It never formats, partitions or erases anything.
          </Notice>
        </>
      )}
    </section>
  );
}
