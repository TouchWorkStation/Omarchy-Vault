// Command vaultd is the Omarchy Vault daemon: it serves the local API and
// dashboard, bound to 127.0.0.1:8788 by default.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/TouchWorkStation/Omarchy-Vault/internal/api"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/auth"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/config"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/demo"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/disks"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/files"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/shortcuts"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/storage"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/sysexec"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/transfer"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/users"
	"github.com/TouchWorkStation/Omarchy-Vault/internal/version"
	"github.com/TouchWorkStation/Omarchy-Vault/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vaultd:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultPath, _ := config.DefaultPath()
	var (
		cfgPath  = flag.String("config", defaultPath, "path to config.json")
		listen   = flag.String("listen", "", "listen address (overrides config; must be loopback unless allowed in config)")
		noSMART  = flag.Bool("no-smart", false, "do not query drive health with smartctl")
		demoMode = flag.Bool("demo", false, "serve sample drives and shortcuts instead of this machine's (for UI development)")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Printf("vaultd %s (commit %s, milestone %d)\n", version.Version, version.Commit, version.Milestone)
		return nil
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// A soft heap limit keeps vaultd small on a desktop: memory from bursts
	// such as password hashing is returned to the OS promptly. It is a
	// target, not a cap; Vault keeps working above it. GOMEMLIMIT overrides.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(48 << 20)
	}

	if os.Geteuid() == 0 {
		log.Warn("vaultd is running as root; it is designed to run as your user (see SECURITY.md)")
	}

	cfg, found, cfgErr := config.Load(*cfgPath)
	if cfgErr != nil {
		// Keep running on safe defaults so the user can see and fix the
		// problem from the dashboard or `vaultctl doctor`.
		log.Error("config invalid; using defaults", "path", *cfgPath, "err", cfgErr)
	}
	if *listen != "" {
		if err := config.ValidateListen(*listen, cfg.Security.AllowNonLoopbackListen); err != nil {
			return err
		}
		cfg.Listen = *listen
	}

	var run sysexec.Runner = sysexec.System{Timeout: 15 * time.Second}
	home, _ := os.UserHomeDir()
	hyprConf := filepath.Join(home, ".config", "hypr", "hyprland.conf")
	cfgPathLive := *cfgPath
	dataLink, err := storage.DefaultLinkPath()
	if err != nil {
		return err
	}
	var mounts func() (storage.MountTable, error)
	secretsDir := auth.SecretsDir(filepath.Dir(*cfgPath))

	if *demoMode {
		// Demo mode never touches this machine's config, drives or links:
		// everything lives in a throwaway sandbox.
		log.Warn("DEMO MODE: sample drives in a temporary sandbox, not this machine's")
		sb, err := demo.NewSandbox()
		if err != nil {
			return err
		}
		defer os.RemoveAll(sb.Dir)
		run = demo.Runner(sb)
		cfgPathLive, dataLink = sb.ConfigPath, sb.DataLink
		secretsDir = auth.SecretsDir(filepath.Dir(sb.ConfigPath))
		listenAddr := cfg.Listen
		cfg, found, cfgErr = config.Default(), false, nil
		cfg.Listen = listenAddr
		sbMounts := storage.MountTable(sb.Mounts)
		mounts = func() (storage.MountTable, error) { return sbMounts, nil }
		if hyprConf, err = demo.HyprConfig(sb.Dir); err != nil {
			return err
		}
	}

	token, err := auth.LoadOrCreateToken(secretsDir)
	if err != nil {
		return fmt.Errorf("local token: %w", err)
	}

	userStore, err := users.Open(filepath.Join(filepath.Dir(cfgPathLive), "users.json"))
	if err != nil {
		return err
	}

	// The file service (SFTPGo). Its state lives beside Vault's own; in
	// demo mode everything stays inside the sandbox.
	dataHome, err := files.DataHome()
	if err != nil {
		return err
	}
	stateBase := filepath.Join(dataHome, "omarchy-vault")
	if *demoMode {
		stateBase = filepath.Dir(dataLink)
	}
	fp := files.Paths{
		ConfigDir:  filepath.Join(stateBase, "sftpgo", "config"),
		DataDir:    filepath.Join(stateBase, "sftpgo", "data"),
		HomesDir:   filepath.Join(stateBase, "homes"),
		SecretsDir: secretsDir,
	}
	if bin, assets, err := files.Locate(dataHome); err == nil {
		fp.Binary, fp.Assets = bin, assets
		log.Info("file service found", "sftpgo", bin)
	} else {
		log.Warn("file service (SFTPGo) not installed; Files is unavailable until it is (run scripts/install.sh)")
	}
	fileMgr := files.NewManager(fp, log)

	// Phone transfers: links live in SQLite (hashes only); the LAN
	// listener exists only while a link is active.
	transfers, err := transfer.Open(filepath.Join(filepath.Dir(cfgPathLive), "vault.db"))
	if err != nil {
		return err
	}
	defer transfers.Close()

	static, built := web.Dist()
	if !built {
		log.Warn("web UI not built into this binary; run `make web && make build`")
		static = nil
	}

	srv := &api.Server{
		Config:      cfg,
		ConfigPath:  cfgPathLive,
		ConfigFound: found,
		ConfigErr:   cfgErr,
		DataLink:    dataLink,
		Mounts:      mounts,
		Auth:        auth.NewLocal(token),
		Users:       userStore,
		Files:       fileMgr,
		Transfers:   transfers,
		Disks:       &disks.Scanner{Run: run, SMART: !*noSMART},
		Run:         run,
		Shortcuts: shortcuts.Inspector{
			Run:        run,
			ConfigPath: hyprConf,
			Home:       home,
		},
		Static:  static,
		Demo:    *demoMode,
		Log:     log,
		Started: time.Now(),
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.PowerOff = stop
	srv.Transfer = &transfer.Server{Store: transfers, Dest: srv.TransferDest, Log: log,
		Host: cfg.Transfer.Host, Port: cfg.Transfer.Port}
	if *demoMode {
		// Demo links are for trying the phone page in a desktop browser.
		srv.Transfer.Host = "127.0.0.1"
	}
	go srv.Transfer.Run(ctx)

	// Background workers: the file service and the storage monitor that
	// starts/stops it as the drive comes and goes.
	fileMgr.OnReady = srv.SyncFiles
	workers := make(chan struct{})
	go func() { fileMgr.Run(ctx); close(workers) }()
	go srv.RunMonitor(ctx, 20*time.Second)
	go srv.RunAutoOff(ctx, 30*time.Second)
	defer func() { stop(); <-workers }()

	errCh := make(chan error, 1)
	go func() {
		log.Info("Omarchy Vault listening", "addr", "http://"+ln.Addr().String(), "version", version.Version)
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
	return nil
}
