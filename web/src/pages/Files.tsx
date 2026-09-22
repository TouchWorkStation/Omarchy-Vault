import type { FilesStatus, Session } from "../api";
import { Link } from "../App";
import { icons } from "../components/Icons";
import { Card, Loading, Notice } from "../components/ui";
import { useApi } from "../useApi";

export function Files() {
  const { data: fs } = useApi<FilesStatus>("/api/files", 15_000);
  const { data: session } = useApi<Session>("/api/session");
  if (!fs) return <Loading what="Files" />;

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>FILES</h1>
          <p className="subtitle">Browse, upload and download everything in your Vault.</p>
        </div>
      </header>

      {fs.state === "running" ? (
        <Card className="setup-cta">
          <div>
            <h2>YOUR FILES</h2>
            <p className="lead">
              {session?.local
                ? "Sign in with your Vault username and password on the next screen."
                : fs.signed_in
                  ? "You're signed in to Files."
                  : "Sign in with your Vault username and password on the next screen."}
            </p>
          </div>
          <a className="btn btn-primary" href={fs.url}>
            {icons.files} OPEN FILES
          </a>
        </Card>
      ) : fs.state === "not_installed" ? (
        <Card title="FILE SERVICE NOT INSTALLED">
          <p className="lead">Vault uses a small file service (SFTPGo) for Files. Install it with:</p>
          <pre className="code-block">cd ~/Omarchy-Vault && ./scripts/install.sh</pre>
          <p className="muted small">The installer builds it from source and never changes your drives.</p>
        </Card>
      ) : fs.state === "waiting_for_storage" ? (
        <Notice tone="warn">
          {fs.message ?? "Files start once your Vault storage is ready."} <Link to="/storage">Check storage</Link>.
        </Notice>
      ) : (
        <Notice tone={fs.state === "error" ? "bad" : "info"}>{fs.message ?? "Files is starting…"}</Notice>
      )}

      <Card title="WHAT YOU CAN DO">
        <ul className="points">
          <li>Upload photos, videos and documents from any browser</li>
          <li>Download single files or whole folders as a zip</li>
          <li>Create, rename and move folders (if your account can write)</li>
          <li>Family and guests only see the folders they were given</li>
        </ul>
      </Card>
    </section>
  );
}
