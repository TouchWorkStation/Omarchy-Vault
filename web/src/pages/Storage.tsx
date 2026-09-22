import type { Disk, DiskStatus, Inventory, Volume } from "../api";
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
          <Badge tone="accent" glyph="✓">
            Usable
          </Badge>
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
            Choosing drives for your Vault arrives in Milestone 2.
          </Notice>
        </>
      )}
    </section>
  );
}
