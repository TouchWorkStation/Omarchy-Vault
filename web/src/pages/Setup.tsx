import { useMemo, useState } from "react";
import { ApiError, send, type Disk, type Inventory, type PoolResponse, type Session, type Status, type Volume } from "../api";
import { Link, navigate } from "../App";
import { icons } from "../components/Icons";
import { Badge, Card, HealthBadge, Kbd, Loading, Notice } from "../components/ui";
import { bytes } from "../format";
import { useApi } from "../useApi";

type Step = "welcome" | "storage" | "vault" | "account" | "remote" | "ready";
const order: Step[] = ["welcome", "storage", "vault", "account", "remote", "ready"];

interface Choice {
  disk: Disk;
  volume: Volume;
}

function Progress({ step }: { step: Step }) {
  const i = order.indexOf(step);
  return (
    <div className="progress" aria-label={`Step ${i + 1} of ${order.length}`}>
      {order.map((s, n) => (
        <span key={s} className={`progress-dot ${n <= i ? "done" : ""}`} />
      ))}
      <span className="muted small">
        {i + 1} / {order.length}
      </span>
    </div>
  );
}

function driveStatus(d: Disk) {
  if (d.system) return <Badge tone="protect" glyph="◆">SYSTEM · Protected</Badge>;
  if (d.adoptable) return <Badge tone="accent" glyph="●">Available</Badge>;
  if (d.status === "unmounted") return <Badge tone="muted" glyph="○">Not mounted</Badge>;
  return <Badge tone="muted" glyph="–">Can't use</Badge>;
}

function StorageStep({
  inv,
  choice,
  setChoice,
  onNext,
  onRescan,
}: {
  inv: Inventory;
  choice: Choice | null;
  setChoice: (c: Choice) => void;
  onNext: () => void;
  onRescan: () => void;
}) {
  const usable = inv.disks.some((d) => d.volumes.some((v) => v.adoptable));
  return (
    <>
      <h1>STORAGE</h1>
      <p className="subtitle">Choose the drive that will hold your Vault.</p>
      {!inv.system_disk_detected && (
        <Notice tone="bad">Vault could not identify the drive Omarchy runs from, so it will not offer any drive.</Notice>
      )}
      <div className="choices" role="radiogroup" aria-label="Drives">
        {inv.disks.map((d) => {
          const vols = d.volumes.filter((v) => v.adoptable);
          if (vols.length === 0) {
            return (
              <div key={d.id} className="choice disabled" aria-disabled="true">
                <div className="choice-main">
                  <strong>{d.display_name}</strong>
                  <span className="muted">{bytes(d.size_bytes)}</span>
                </div>
                {driveStatus(d)}
                {!d.system && d.status === "unmounted" && (
                  <p className="muted small choice-note">Mount it with your file manager, then rescan.</p>
                )}
              </div>
            );
          }
          return vols.map((v) => {
            const selected = choice?.volume.name === v.name;
            return (
              <button
                key={v.name}
                role="radio"
                aria-checked={selected}
                className={`choice ${selected ? "selected" : ""}`}
                onClick={() => setChoice({ disk: d, volume: v })}
              >
                <div className="choice-main">
                  <strong>{d.display_name}</strong>
                  <span className="muted">
                    {bytes(v.fs_size_bytes ?? v.size_bytes)} · {bytes(v.fs_avail_bytes)} free
                  </span>
                </div>
                <div className="choice-badges">
                  {driveStatus(d)}
                  <HealthBadge status={d.health.status} />
                </div>
                <p className="muted small choice-note">
                  {v.mountpoints[0]} · {v.fstype}
                </p>
              </button>
            );
          });
        })}
      </div>
      {!usable && <Notice>No drive is ready yet. Plug one in and mount it with your file manager, then rescan.</Notice>}
      <div className="setup-actions">
        <button className="btn" onClick={onRescan}>
          {icons.refresh} Rescan
        </button>
        <button className="btn btn-primary" disabled={!choice} onClick={onNext}>
          Select Drive
        </button>
      </div>
    </>
  );
}

function VaultStep({
  choice,
  status,
  session,
  onBack,
  onDone,
}: {
  choice: Choice;
  status: Status | null;
  session: Session | null;
  onBack: () => void;
  onDone: (r: PoolResponse) => void;
}) {
  const [whole, setWhole] = useState(false);
  const [folders, setFolders] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const v = choice.volume;
  const replacing = status?.storage.configured ?? false;
  const mount = v.mountpoints[0];

  async function create() {
    setBusy(true);
    setError(null);
    try {
      const r = await send<PoolResponse>("POST", "/api/pool", {
        volume: v.name,
        folder: whole ? "" : "Vault",
        create_folders: folders,
        replace: replacing,
      });
      onDone(r);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <h1>VAULT STORAGE</h1>
      <div className="big-number">{bytes(v.fs_avail_bytes)}</div>
      <p className="subtitle">
        available on {choice.disk.display_name} · {bytes(v.fs_size_bytes ?? v.size_bytes)} drive
      </p>

      <Card title="USE AS">
        <div className="option-list">
          <label className="option">
            <input type="radio" name="use" checked readOnly />
            <span>
              <strong>One Vault</strong>
              <span className="muted small">Combining several drives arrives in Milestone 7.</span>
            </span>
          </label>
        </div>
      </Card>

      <Card title="WHERE ON THE DRIVE">
        <div className="option-list">
          <label className="option">
            <input type="radio" name="where" checked={!whole} onChange={() => setWhole(false)} />
            <span>
              <strong>A Vault folder</strong> <span className="muted">(recommended)</span>
              <span className="muted small mono">{mount}/Vault</span>
            </span>
          </label>
          <label className="option">
            <input type="radio" name="where" checked={whole} onChange={() => setWhole(true)} />
            <span>
              <strong>The whole drive</strong>
              <span className="muted small mono">{mount}</span>
            </span>
          </label>
          <label className="option">
            <input type="checkbox" checked={folders} onChange={(e) => setFolders(e.target.checked)} />
            <span>
              <strong>Create folders</strong>
              <span className="muted small">Photos · Documents · Backups · Projects · Phone Uploads · Shared (only if missing)</span>
            </span>
          </label>
        </div>
      </Card>

      {v.notes?.map((n) => (
        <Notice key={n} tone="warn">
          {n}
        </Notice>
      ))}
      {choice.disk.removable && <Notice tone="warn">This is a removable drive. Your Vault is offline whenever it is unplugged.</Notice>}
      {replacing && (
        <Notice tone="warn">
          This replaces your current storage ({status?.storage.sources[0]?.data_dir}). Files there stay where they are.
        </Notice>
      )}
      <Notice>Nothing is formatted, moved or deleted. Existing files on the drive are left as they are.</Notice>
      {session && !session.can_change && <Notice tone="warn">{session.hint}</Notice>}
      {error && <Notice tone="bad">{error}</Notice>}

      <div className="setup-actions">
        <button className="btn" onClick={onBack} disabled={busy}>
          Back
        </button>
        <button className="btn btn-primary" onClick={create} disabled={busy || !session?.can_change}>
          {busy ? "Creating…" : "Create Vault"}
        </button>
      </div>
    </>
  );
}

export function Setup() {
  const params = new URLSearchParams(window.location.search);
  const [step, setStep] = useState<Step>(params.get("volume") ? "storage" : "welcome");
  const [refresh, setRefresh] = useState(0);
  const { data: inv, error } = useApi<Inventory>(`/api/disks${refresh ? `?refresh=1&n=${refresh}` : ""}`);
  const { data: status } = useApi<Status>("/api/status");
  const { data: session } = useApi<Session>("/api/session");
  const [picked, setPicked] = useState<Choice | null>(null);
  const [result, setResult] = useState<PoolResponse | null>(null);

  // Preselect ?volume=sda1 (from the Storage page) once drives are known.
  const preselected = useMemo(() => {
    const want = params.get("volume");
    if (!want || !inv) return null;
    for (const d of inv.disks) for (const v of d.volumes) if (v.name === want && v.adoptable) return { disk: d, volume: v };
    return null;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inv]);
  const choice = picked ?? preselected;

  let body;
  switch (step) {
    case "welcome":
      body = (
        <div className="welcome">
          <div className="welcome-mark" aria-hidden="true">
            ▣
          </div>
          <h1>Welcome to Vault</h1>
          <p className="lead">Turn your Omarchy machine into private cloud storage.</p>
          <p className="muted">
            Vault uses a drive you already have. It never formats, partitions or erases anything.
          </p>
          <div className="setup-actions">
            <button className="btn btn-primary" onClick={() => setStep("storage")}>
              Get Started
            </button>
          </div>
        </div>
      );
      break;
    case "storage":
      body = error ? (
        <Notice tone="bad">{error}</Notice>
      ) : !inv ? (
        <Loading what="drives" />
      ) : (
        <StorageStep
          inv={inv}
          choice={choice}
          setChoice={setPicked}
          onRescan={() => setRefresh((n) => n + 1)}
          onNext={() => setStep("vault")}
        />
      );
      break;
    case "vault":
      body = choice ? (
        <VaultStep
          choice={choice}
          status={status}
          session={session}
          onBack={() => setStep("storage")}
          onDone={(r) => {
            setResult(r);
            setStep("account");
          }}
        />
      ) : (
        <Notice>Choose a drive first.</Notice>
      );
      break;
    case "account":
      body = (
        <>
          <h1>ACCOUNT</h1>
          <Card>
            <p className="lead">Sign-in and user accounts arrive in Milestone 3.</p>
            <p className="muted">
              Until then, Vault only accepts changes from this computer, and only from you. Opening Vault with{" "}
              <Kbd combo="Super + Shift + V" /> or <code>vaultctl open</code> signs you in.
            </p>
            <dl className="kv">
              <dt>2FA</dt>
              <dd>Set up later</dd>
            </dl>
          </Card>
          <div className="setup-actions">
            <button className="btn btn-primary" onClick={() => setStep("remote")}>
              Continue
            </button>
          </div>
        </>
      );
      break;
    case "remote":
      body = (
        <>
          <h1>REMOTE ACCESS</h1>
          <Card>
            <p className="lead">Reach your Vault from anywhere at your own domain.</p>
            <p className="muted">
              Vault stays private to this computer until you turn this on. Connecting Cloudflare arrives in Milestone 6.
            </p>
          </Card>
          <div className="setup-actions">
            <button className="btn" disabled>
              Connect Cloudflare
            </button>
            <button className="btn btn-primary" onClick={() => setStep("ready")}>
              Skip for now
            </button>
          </div>
        </>
      );
      break;
    case "ready": {
      const st = result?.storage ?? status?.storage;
      body = (
        <div className="welcome">
          <h1>YOUR VAULT IS READY</h1>
          <div className="big-number">{bytes(st?.free_bytes)}</div>
          <p className="subtitle">Available</p>
          {result?.result && (
            <p className="muted small mono">
              {result.result.data_dir}
              {result.result.created.length > 0 && ` · created ${result.result.created.join(", ")}`}
            </p>
          )}
          {result?.result?.skipped.length ? (
            <Notice tone="warn">Left alone because they are not plain folders: {result.result.skipped.join(", ")}.</Notice>
          ) : null}
          {result?.notes?.map((n) => (
            <Notice key={n}>{n}</Notice>
          ))}
          {st?.root_link.state === "missing" && (
            <Notice>
              Optional: run <code>vaultctl link</code> once to also reach your Vault at <code>/srv/vault</code>.
            </Notice>
          )}
          <div className="setup-actions center">
            <Link to="/files" className="btn btn-primary">
              {icons.files} Open Files
            </Link>
          </div>
          <p className="muted small">Quick Actions</p>
          <div className="setup-actions center">
            <Link to="/upload" className="btn">
              {icons.upload} Upload
            </Link>
            <Link to="/download" className="btn">
              {icons.download} Download
            </Link>
          </div>
          <p>
            <a
              href="/"
              onClick={(e) => {
                e.preventDefault();
                navigate("/");
              }}
            >
              Go to the dashboard
            </a>
          </p>
        </div>
      );
      break;
    }
  }

  return (
    <section className="page setup">
      <Progress step={step} />
      {body}
    </section>
  );
}
