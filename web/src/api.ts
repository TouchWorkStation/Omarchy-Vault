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

export type Role = "admin" | "family" | "guest";

export interface FolderGrant {
  name: string;
  access: "rw" | "ro";
}

export interface UserView {
  username: string;
  role: Role;
  disabled: boolean;
  folders: FolderGrant[];
  all_folders: boolean;
  totp_enabled: boolean;
  created_at: string;
}

export interface Session {
  signed_in: boolean;
  can_change: boolean;
  user?: UserView;
  local: boolean;
  accounts_exist: boolean;
  files_signed_in: boolean;
  demo?: boolean;
  hint?: string;
}

export interface UsersResponse {
  users: UserView[];
  folders: string[];
}

export interface FilesStatus {
  state: "not_installed" | "waiting_for_storage" | "starting" | "running" | "error";
  installed: boolean;
  running: boolean;
  url: string;
  message?: string;
  signed_in?: boolean;
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
  phone: { active_links: number; listening: boolean };
  auto_off_minutes: number;
  users: { count: number };
  files: FilesStatus;
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
  };
  config_found: boolean;
  config_error?: string;
  read_only: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code = "",
  ) {
    super(message);
  }
}

export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return request<T>("GET", path, undefined, signal);
}

// Called when the server says the session is gone, so the app can show the
// sign-in screen instead of a wall of errors.
let onSignedOut: (() => void) | undefined;
export function setSignedOutHandler(fn: () => void) {
  onSignedOut = fn;
}

// Writes carry X-Vault-Request so a cross-site page can never forge them.
export async function send<T>(method: "POST" | "PUT" | "DELETE", path: string, body?: unknown): Promise<T> {
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
    let code = "";
    try {
      const body = await res.json();
      if (body?.message) msg = body.message;
      if (body?.error) code = body.error;
    } catch {
      /* not JSON */
    }
    if (res.status === 401 && code === "login_required") onSignedOut?.();
    throw new ApiError(res.status, msg, code);
  }
  return res.json() as Promise<T>;
}

export interface TransferItem {
  at: string;
  kind: string;
  folder: string;
  name: string;
  size: number;
  actor: string;
}

export interface LinkView {
  id: string;
  kind: "upload" | "download" | "share";
  folder: string;
  path?: string;
  max_files: number;
  has_password: boolean;
  created_by: string;
  client: string;
  created_at: string;
  expires_at: string;
  revoked: boolean;
  files: number;
  bytes: number;
  state: "active" | "expired" | "stopped" | "full";
  url?: string;
  qr_svg?: string;
  received: TransferItem[];
}

export interface BrowseEntry {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  modified: string;
}

export interface BrowseResponse {
  path: string;
  entries: BrowseEntry[];
  truncated: boolean;
}

// Links above this many downloads are unlimited.
export const UNLIMITED = 2 ** 30;
