import type { ReactNode } from "react";
import type { HealthStatus } from "../api";
import { pct } from "../format";

export function Card({
  title,
  children,
  actions,
  className = "",
}: {
  title?: ReactNode;
  children: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <section className={`card ${className}`}>
      {(title || actions) && (
        <header className="card-head">
          {title && <h2>{title}</h2>}
          {actions}
        </header>
      )}
      {children}
    </section>
  );
}

type Tone = "good" | "warn" | "bad" | "muted" | "accent" | "protect";

// Status is never color alone: every badge carries a glyph and a word.
export function Badge({ tone, glyph, children }: { tone: Tone; glyph?: string; children: ReactNode }) {
  return (
    <span className={`badge badge-${tone}`}>
      {glyph && <span aria-hidden="true">{glyph}</span>}
      {children}
    </span>
  );
}

const healthMap: Record<HealthStatus, { tone: Tone; glyph: string; label: string }> = {
  healthy: { tone: "good", glyph: "●", label: "Healthy" },
  warning: { tone: "warn", glyph: "▲", label: "Warning" },
  critical: { tone: "bad", glyph: "✕", label: "Critical" },
  unknown: { tone: "muted", glyph: "?", label: "Unknown" },
};

export function HealthBadge({ status }: { status: HealthStatus }) {
  const h = healthMap[status] ?? healthMap.unknown;
  return (
    <Badge tone={h.tone} glyph={h.glyph}>
      {h.label}
    </Badge>
  );
}

// Single-series capacity meter. The number sits beside it in text ink.
export function Meter({ used, total, label }: { used: number; total: number; label: string }) {
  const p = pct(used, total);
  const tone = p >= 95 ? "bad" : p >= 85 ? "warn" : "ok";
  return (
    <div
      className={`meter meter-${tone}`}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(p)}
    >
      <div className="meter-fill" style={{ width: `${p}%` }} />
    </div>
  );
}

export function Stat({ label, value, sub }: { label: string; value: ReactNode; sub?: ReactNode }) {
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      {sub && <div className="stat-sub">{sub}</div>}
    </div>
  );
}

export function Kbd({ combo }: { combo: string }) {
  const parts = combo.split(" + ");
  return (
    <span className="kbd-combo">
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 && <span className="kbd-plus">+</span>}
          <kbd>{p}</kbd>
        </span>
      ))}
    </span>
  );
}

export function Notice({ tone = "info", children }: { tone?: "info" | "warn" | "bad"; children: ReactNode }) {
  const glyph = tone === "info" ? "·" : tone === "warn" ? "!" : "✕";
  return (
    <div className={`notice notice-${tone}`} role={tone === "info" ? "status" : "alert"}>
      <span className="notice-glyph" aria-hidden="true">
        {glyph}
      </span>
      <div>{children}</div>
    </div>
  );
}

export function Loading({ what }: { what: string }) {
  return <p className="muted loading">Loading {what}…</p>;
}
