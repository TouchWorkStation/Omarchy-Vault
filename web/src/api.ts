// Types mirror internal/api and internal/disks. Keep them in sync.

export type HealthStatus = "healthy" | "warning" | "critical" | "unknown";
export type DiskStatus = "system" | "available" | "unmounted" | "unsupported" | "read-only";

export interface Health {
  status: HealthStatus;
  smart_passed?: boolean;
  temperature_c?: number;
  power_on_hours?: number;
  reallocated_sectors?: number;
  pending_sectors?: number;
  media_errors?: number;
  percent_used?: number;
  reasons?: string[];
  message?: string;
}

export interface Volume {
  name: string;
  path: string;
  type: string;
  fstype?: string;
  label?: string;
  size_bytes: number;
  mountpoints: string[];
  fs_size_bytes?: number;
  fs_used_bytes?: number;
  fs_avail_bytes?: number;
  status: string;
  adoptable: boolean;
  notes?: string[];
}

export interface Disk {
  id: string;
  name: string;
  path: string;
  display_name: string;
  model?: string;
  kind: string;
  transport?: string;
  size_bytes: number;
  removable: boolean;
  read_only: boolean;
  system: boolean;
  protected: boolean;
  protected_reason?: string;
  status: DiskStatus;
  adoptable: boolean;
  volumes: Volume[];
  health: Health;
  notes?: string[];
}

export interface DriveSummary {
  total: number;
  system: number;
  available: number;
  unmounted: number;
  unsupported: number;
  healthy: number;
  warning: number;
  critical: number;
  unknown: number;
}

export interface Inventory {
  disks: Disk[];
  system_disk_detected: boolean;
  summary: DriveSummary;
  warnings?: string[];
  scanned_at: string;
}

export type StorageState = "not_set_up" | "ready" | "drive_missing" | "drive_moved" | "problem";

export interface SourceStatus {
  label: string;
  volume?: string;
  path: string;
  folder?: string;
  data_dir: string;
  state: StorageState;
  current_mount?: string;
  message?: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
}

export interface StorageStatus {
  state: StorageState;
  configured: boolean;
  root: string;
  root_link: { path: string; state: "ok" | "missing" | "elsewhere" | "not_link"; target?: string };
  data_link: string;
  pool_mode: string;
  sources: SourceStatus[];
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
  message?: string;
}

export interface AdoptResult {
  data_dir: string;
  created: string[];
  existing: string[];
  skipped: string[];
}

export interface PoolResponse {
  storage: StorageStatus;
  result?: AdoptResult;
  notes?: string[];
}

export interface Session {
  can_change: boolean;
  hint?: string;
}

export interface Candidate {
  disk: string;
  display_name: string;
  volume: string;
  mountpoint: string;
  fstype: string;
  size_bytes: number;
  free_bytes: number;
  health: HealthStatus;
}

export interface StorageResponse extends StorageStatus {
  candidates: Candidate[];
}

export interface Service {
  id: string;
  name: string;
  purpose: string;
  unit?: string;
  package: string;
  milestone: number;
  required: boolean;
  installed: boolean;
  unit_state?: string;
}

export interface ShortcutBrief {
  id: string;
  label: string;
  combo: string;
  status: "available" | "conflict" | "installed" | "unknown";
}

export interface Status {
  name: string;
  demo?: boolean;
  version: string;
  milestone: number;
  hostname: string;
  uptime_seconds: number;
  listen: string;
  setup_complete: boolean;
  storage: StorageStatus;
  drives: DriveSummary | null;
  drives_error?: string;
  system_disk_detected: boolean;
  remote: { enabled: boolean; domain?: string; state: string; milestone: number };
  users: { count: number | null; milestone: number };
  services: Service[];
  shortcuts: ShortcutBrief[];
  warnings?: string[];
}

export interface Binding {
  mods: string[];
  key: string;
  dispatcher: string;
  arg?: string;
  description?: string;
  source: string;
}

export interface ShortcutCheck {
  id: string;
  label: string;
  direction: string;
  combo: string;
  binding_line: string;
  status: ShortcutBrief["status"];
  conflicts?: Binding[];
  suggestion?: { mods: string[]; key: string };
  options?: string[];
}

export interface ShortcutReport {
  sources: string[];
  checks: ShortcutCheck[];
  warnings?: string[];
  installed: boolean;
}

export interface SettingsResponse {
  config: {
    vault_root: string;
    listen: string;
    pool: { mode: string };
    preferences: {
      upload_folder: string;
      upload_expiry_minutes: number;
      download_expiry_minutes: number;
      download_max_count: number;
    };
    remote: { enabled: boolean; provider?: string; domain?: string };
  };
  config_found: boolean;
  config_error?: string;
  read_only: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return request<T>("GET", path, undefined, signal);
}

// Writes carry X-Vault-Request so a cross-site page can never forge them.
export async function send<T>(method: "POST" | "DELETE", path: string, body?: unknown): Promise<T> {
  return request<T>(method, path, body);
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response;
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method !== "GET") headers["X-Vault-Request"] = "1";
  if (body !== undefined) headers["Content-Type"] = "application/json";
  try {
    res = await fetch(path, {
      method,
      signal,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (e) {
    if ((e as Error).name === "AbortError") throw e;
    throw new ApiError(0, "Vault service is not reachable.");
  }
  if (!res.ok) {
    let msg = `Request failed (${res.status})`;
    try {
      const body = await res.json();
      if (body?.message) msg = body.message;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  return res.json() as Promise<T>;
}
