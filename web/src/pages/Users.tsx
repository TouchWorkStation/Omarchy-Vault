import { useState } from "react";
import { ApiError, send, type FolderGrant, type Role, type Session, type UserView, type UsersResponse } from "../api";
import { Badge, Card, Loading, Notice } from "../components/ui";
import { useApi } from "../useApi";

const roleInfo: Record<Role, { label: string; about: string }> = {
  admin: { label: "Admin", about: "Manages Vault and sees every file." },
  family: { label: "Family", about: "Reads and writes the folders you choose." },
  guest: { label: "Guest", about: "Can only view and download the folders you choose." },
};

function defaultGrants(role: Role, folders: string[]): FolderGrant[] {
  if (role === "family") return folders.map((name) => ({ name, access: "rw" }));
  if (role === "guest") return folders.filter((f) => f === "Shared").map((name) => ({ name, access: "ro" }));
  return [];
}

function FolderPicker({
  role,
  folders,
  value,
  onChange,
}: {
  role: Role;
  folders: string[];
  value: FolderGrant[];
  onChange: (v: FolderGrant[]) => void;
}) {
  if (role === "admin") return <p className="muted small">Admins can open every folder.</p>;
  if (folders.length === 0) return <p className="muted small">Set up Vault storage first to choose folders.</p>;
  const get = (name: string) => value.find((g) => g.name === name);
  function toggle(name: string, on: boolean) {
    onChange(on ? [...value, { name, access: role === "guest" ? "ro" : "rw" }] : value.filter((g) => g.name !== name));
  }
  function setAccess(name: string, access: "rw" | "ro") {
    onChange(value.map((g) => (g.name === name ? { ...g, access } : g)));
  }
  return (
    <div className="folder-picker">
      {folders.map((name) => {
        const g = get(name);
        return (
          <div key={name} className="folder-row">
            <label className="option">
              <input type="checkbox" checked={!!g} onChange={(e) => toggle(name, e.target.checked)} />
              <span>{name}</span>
            </label>
            {g && role !== "guest" && (
              <select value={g.access} onChange={(e) => setAccess(name, e.target.value as "rw" | "ro")} aria-label={`${name} access`}>
                <option value="rw">Read & write</option>
                <option value="ro">Read only</option>
              </select>
            )}
            {g && role === "guest" && <span className="muted small">Read only</span>}
          </div>
        );
      })}
    </div>
  );
}

function CreateUser({ folders, onCreated }: { folders: string[]; onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("family");
  const [grants, setGrants] = useState<FolderGrant[]>(defaultGrants("family", folders));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (!open) {
    return (
      <div className="setup-actions">
        <button
          className="btn btn-primary"
          onClick={() => {
            setGrants(defaultGrants(role, folders));
            setOpen(true);
          }}
        >
          Create user
        </button>
      </div>
    );
  }
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await send("POST", "/api/users", { username, password, role, folders: grants });
      setOpen(false);
      setUsername("");
      setPassword("");
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card title="CREATE USER">
      <form className="form" onSubmit={submit}>
        <div className="form-row">
          <label className="field">
            <span>Username</span>
            <input
              value={username}
              onChange={(e) => setUsername(e.target.value.toLowerCase())}
              autoCapitalize="none"
              spellCheck={false}
              pattern="[a-z][a-z0-9_\-]{1,31}"
              title="2–32 lowercase letters, digits, - or _"
              required
            />
          </label>
          <label className="field">
            <span>Password (10+ characters)</span>
            <input type="password" autoComplete="new-password" minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} required />
          </label>
        </div>
        <div className="role-picker" role="radiogroup" aria-label="Role">
          {(Object.keys(roleInfo) as Role[]).map((r) => (
            <label key={r} className={`option role-option ${role === r ? "selected" : ""}`}>
              <input
                type="radio"
                name="role"
                checked={role === r}
                onChange={() => {
                  setRole(r);
                  setGrants(defaultGrants(r, folders));
                }}
              />
              <span>
                <strong>{roleInfo[r].label}</strong>
                <span className="muted small">{roleInfo[r].about}</span>
              </span>
            </label>
          ))}
        </div>
        <h2>FOLDERS</h2>
        <FolderPicker role={role} folders={folders} value={grants} onChange={setGrants} />
        {error && <Notice tone="bad">{error}</Notice>}
        <div className="setup-actions">
          <button type="button" className="btn" onClick={() => setOpen(false)}>
            Cancel
          </button>
          <button className="btn btn-primary" disabled={busy}>
            {busy ? "Creating…" : "Create user"}
          </button>
        </div>
      </form>
    </Card>
  );
}

function UserRow({ u, folders, me, onChanged }: { u: UserView; folders: string[]; me?: string; onChanged: () => void }) {
  const [mode, setMode] = useState<"" | "folders" | "password" | "remove">("");
  const [grants, setGrants] = useState<FolderGrant[]>(u.folders);
  const [password, setPassword] = useState("");
  const [msg, setMsg] = useState<{ tone: "info" | "bad"; text: string } | null>(null);

  async function act(fn: () => Promise<unknown>, ok: string) {
    setMsg(null);
    try {
      await fn();
      setMsg({ tone: "info", text: ok });
      setMode("");
      onChanged();
    } catch (err) {
      setMsg({ tone: "bad", text: err instanceof ApiError ? err.message : "Something went wrong." });
    }
  }

  return (
    <li className="user-row">
      <div className="user-head">
        <div>
          <strong>{u.username}</strong>
          {u.username === me && <span className="muted small"> (you)</span>}
          <div className="muted small">
            {u.all_folders ? "All folders" : u.folders.length ? u.folders.map((f) => `${f.name}${f.access === "ro" ? " (read only)" : ""}`).join(" · ") : "No folders"}
          </div>
        </div>
        <div className="user-badges">
          <Badge tone={u.role === "admin" ? "protect" : "muted"} glyph={u.role === "admin" ? "◆" : "·"}>
            {roleInfo[u.role].label}
          </Badge>
          {u.totp_enabled && <Badge tone="good" glyph="●">2FA</Badge>}
          {u.disabled && <Badge tone="bad" glyph="✕">Disabled</Badge>}
        </div>
      </div>
      <div className="confirm-row">
        {u.role !== "admin" && (
          <button className="btn btn-small" onClick={() => setMode(mode === "folders" ? "" : "folders")}>
            Folders
          </button>
        )}
        <button className="btn btn-small" onClick={() => setMode(mode === "password" ? "" : "password")}>
          Reset password
        </button>
        {u.username !== me && (
          <>
            <button
              className="btn btn-small"
              onClick={() => act(() => send("PUT", `/api/users/${encodeURIComponent(u.username)}`, { disabled: !u.disabled }), u.disabled ? "Enabled." : "Disabled. They were signed out.")}
            >
              {u.disabled ? "Enable" : "Disable"}
            </button>
            <button className="btn btn-small btn-danger" onClick={() => setMode(mode === "remove" ? "" : "remove")}>
              Remove
            </button>
          </>
        )}
      </div>
      {mode === "folders" && (
        <div className="user-edit">
          <FolderPicker role={u.role} folders={folders} value={grants} onChange={setGrants} />
          <div className="setup-actions">
            <button className="btn btn-small btn-primary" onClick={() => act(() => send("PUT", `/api/users/${encodeURIComponent(u.username)}`, { folders: grants }), "Folders updated.")}>
              Save folders
            </button>
          </div>
        </div>
      )}
      {mode === "password" && (
        <form
          className="user-edit form-row"
          onSubmit={(e) => {
            e.preventDefault();
            act(() => send("POST", `/api/users/${encodeURIComponent(u.username)}/password`, { password }), "Password reset. They were signed out everywhere.");
            setPassword("");
          }}
        >
          <label className="field">
            <span>New password for {u.username}</span>
            <input type="password" autoComplete="new-password" minLength={10} value={password} onChange={(e) => setPassword(e.target.value)} required />
          </label>
          <button className="btn btn-small btn-primary">Set password</button>
        </form>
      )}
      {mode === "remove" && (
        <div className="user-edit confirm-row">
          <span>Remove {u.username}? Their files stay in the Vault.</span>
          <button className="btn btn-small" onClick={() => setMode("")}>
            Cancel
          </button>
          <button className="btn btn-small btn-danger" onClick={() => act(() => send("DELETE", `/api/users/${encodeURIComponent(u.username)}`), "Removed.")}>
            Remove user
          </button>
        </div>
      )}
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
    </li>
  );
}

export function Users() {
  const { data, error, reload } = useApi<UsersResponse>("/api/users");
  const { data: session } = useApi<Session>("/api/session");
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>USERS</h1>
          <p className="subtitle">Everyone signs in with their own username and password.</p>
        </div>
      </header>
      {error && <Notice tone="bad">{error}</Notice>}
      {!data && !error && <Loading what="users" />}
      {data && (
        <>
          <CreateUser folders={data.folders} onCreated={reload} />
          <Card title={`${data.users.length} ${data.users.length === 1 ? "USER" : "USERS"}`}>
            <ul className="user-list">
              {data.users.map((u) => (
                <UserRow key={u.username} u={u} folders={data.folders} me={session?.user?.username} onChanged={reload} />
              ))}
            </ul>
          </Card>
        </>
      )}
    </section>
  );
}
