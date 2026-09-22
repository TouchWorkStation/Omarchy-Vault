// Decimal units, matching how drives are sold ("8 TB").
export function bytes(n: number | undefined | null): string {
  if (n == null || !isFinite(n)) return "—";
  if (n < 1000) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB", "PB"];
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

export function pct(used: number, total: number): number {
  if (!total) return 0;
  return Math.min(100, Math.max(0, (used / total) * 100));
}

export function hours(h?: number): string {
  if (h == null) return "—";
  if (h < 48) return `${h} h`;
  const d = Math.round(h / 24);
  return d > 730 ? `${(d / 365).toFixed(1)} years` : `${d} days`;
}
