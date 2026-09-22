import { useEffect, useState, type ReactNode } from "react";
import { icons, type IconName } from "./components/Icons";
import { Home } from "./pages/Home";
import { Storage } from "./pages/Storage";
import { Settings } from "./pages/Settings";
import { Planned } from "./pages/Planned";
import type { Status } from "./api";
import { useApi } from "./useApi";

export interface Route {
  path: string;
  label: string;
  icon: IconName;
}

export const routes: Route[] = [
  { path: "/", label: "Home", icon: "home" },
  { path: "/files", label: "Files", icon: "files" },
  { path: "/storage", label: "Storage", icon: "storage" },
  { path: "/upload", label: "Upload", icon: "upload" },
  { path: "/download", label: "Download", icon: "download" },
  { path: "/users", label: "Users", icon: "users" },
  { path: "/remote", label: "Remote Access", icon: "remote" },
  { path: "/settings", label: "Settings", icon: "settings" },
];

export function navigate(path: string) {
  if (path === window.location.pathname) return;
  window.history.pushState({}, "", path);
  window.dispatchEvent(new PopStateEvent("popstate"));
  window.scrollTo(0, 0);
}

function usePath() {
  const [path, setPath] = useState(window.location.pathname);
  useEffect(() => {
    const on = () => setPath(window.location.pathname);
    window.addEventListener("popstate", on);
    return () => window.removeEventListener("popstate", on);
  }, []);
  return path;
}

export function Link({ to, className, children }: { to: string; className?: string; children: ReactNode }) {
  return (
    <a
      href={to}
      className={className}
      onClick={(e) => {
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
        e.preventDefault();
        navigate(to);
      }}
    >
      {children}
    </a>
  );
}

function Page({ path }: { path: string }) {
  switch (path) {
    case "/":
      return <Home />;
    case "/storage":
      return <Storage />;
    case "/settings":
      return <Settings />;
    case "/files":
      return (
        <Planned
          title="FILES"
          milestone={3}
          lead="Browse, upload and download everything in your Vault from any browser."
          points={["OPEN FILES from here or from your phone", "Folders for Photos, Documents, Backups and more", "SFTP and WebDAV for power users (off by default)"]}
        />
      );
    case "/upload":
      return (
        <Planned
          title="UPLOAD TO VAULT"
          subtitle="Phone → Vault"
          milestone={4}
          combo="Super + Shift + U"
          lead="Press the shortcut, scan the QR code with your phone, pick photos, videos or files. They land in Phone Uploads."
          points={["No app needed on the phone", "Link expires after 10 minutes", "Upload-only: the phone never sees the rest of your Vault"]}
        />
      );
    case "/download":
      return (
        <Planned
          title="DOWNLOAD FROM VAULT"
          subtitle="Vault → Phone"
          milestone={5}
          combo="Super + Shift + D"
          lead="Press the shortcut, pick a file or folder, scan the QR code, and it downloads straight to your phone."
          points={["One file or a whole folder", "Link works once and expires after 10 minutes", "Your folder structure stays private"]}
        />
      );
    case "/users":
      return (
        <Planned
          title="USERS"
          milestone={3}
          lead="Give family and guests their own sign-in with just the folders they need."
          points={["Admin · Family · Guest", "Read/write or read-only per folder", "Reset passwords and disable accounts"]}
        />
      );
    case "/remote":
      return (
        <Planned
          title="REMOTE ACCESS"
          milestone={6}
          lead="Reach your Vault at your own domain from anywhere, through a Cloudflare tunnel. Nothing is opened on your router."
          points={["Connect your domain", "TLS handled for you", "Off until you turn it on"]}
        />
      );
    default:
      return (
        <section className="page">
          <h1>NOT FOUND</h1>
          <p className="muted">
            There is nothing here. <Link to="/">Go home</Link>.
          </p>
        </section>
      );
  }
}

export function App() {
  const path = usePath();
  const { data: status } = useApi<Status>("/api/status");
  useEffect(() => {
    const r = routes.find((x) => x.path === path);
    document.title = r && r.path !== "/" ? `${r.label} · Vault` : "Omarchy Vault";
  }, [path]);

  return (
    <div className="shell">
      <aside className="sidebar">
        <Link to="/" className="brand">
          <span className="brand-mark" aria-hidden="true">
            ▣
          </span>
          <span>VAULT</span>
        </Link>
        <nav aria-label="Primary">
          {routes.map((r) => (
            <Link key={r.path} to={r.path} className={`nav-item ${path === r.path ? "active" : ""}`}>
              {icons[r.icon]}
              <span>{r.label}</span>
            </Link>
          ))}
        </nav>
        <p className="tagline">
          Beam moves it.
          <br />
          Vault keeps it.
        </p>
      </aside>
      <main className="main" aria-live="polite">
        {status?.demo && (
          <div className="demo-banner" role="status">
            DEMO MODE · sample drives, not this computer's
          </div>
        )}
        <Page path={path} />
      </main>
      <nav className="tabbar" aria-label="Primary">
        {routes
          .filter((r) => ["/", "/files", "/upload", "/download", "/storage"].includes(r.path))
          .map((r) => (
            <Link key={r.path} to={r.path} className={`tab ${path === r.path ? "active" : ""}`}>
              {icons[r.icon]}
              <span>{r.label}</span>
            </Link>
          ))}
        <Link to="/settings" className={`tab ${["/settings", "/users", "/remote"].includes(path) ? "active" : ""}`}>
          {icons.settings}
          <span>More</span>
        </Link>
      </nav>
    </div>
  );
}
