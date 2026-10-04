package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/niceit/s3-anysync/internal/agent"
	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/ipc"
	"github.com/niceit/s3-anysync/internal/logging"
	"github.com/niceit/s3-anysync/internal/notify"
	"github.com/niceit/s3-anysync/internal/secrets"
	"github.com/niceit/s3-anysync/internal/tray"
)

var errAgentRunning = errors.New("another agent or command holds the data folder")

// agentEnv is what runAgent needs from the outside world; tests swap parts.
type agentEnv struct {
	dataDir      string
	secrets      secrets.Store
	autostart    agent.Autostart
	notifier     notify.Notifier
	configure    func(*app.App) // adjusts the App (tests: memory storage)
	tray         bool
	verbose      bool
	noNotify     bool           // log notifications instead of posting them (development)
	pollInterval time.Duration  // tests only
	level        *slog.LevelVar // tests: observe the log level; nil makes a new one
	stderr       io.Writer
}

// runAgent runs the background agent until ctx ends or Quit is picked in the tray.
func runAgent(ctx context.Context, env agentEnv) error {
	lock, err := datalock.Acquire(env.dataDir)
	if errors.Is(err, datalock.ErrBusy) {
		return errAgentRunning
	}
	if err != nil {
		return err
	}
	defer lock.Release()

	logs, err := logging.OpenDaily(filepath.Join(env.dataDir, "logs"), 7, nil)
	if err != nil {
		return err
	}
	defer logs.Close()
	level := env.level
	if level == nil {
		level = new(slog.LevelVar)
	}
	var w io.Writer = logs
	if env.verbose && env.stderr != nil {
		w = io.MultiWriter(logs, env.stderr)
	}
	log := logging.New(w, level)

	notifier := env.notifier
	if env.noNotify {
		notifier = pickNotifier(true, log)
	}

	a, err := app.Open(env.dataDir, env.secrets, log)
	if err != nil {
		return err
	}
	defer a.Close()
	if env.configure != nil {
		env.configure(a)
	}
	if name, err := a.LogLevel(); err == nil {
		if l, err := logging.ParseLevel(name); err == nil {
			level.Set(l)
		}
	}

	m, err := agent.New(agent.Options{
		App: a, Notifier: notifier, Autostart: env.autostart, Triggers: true,
		PollInterval: env.pollInterval, LogLevel: level, Version: version, Log: log,
	})
	if err != nil {
		return err
	}
	if err := m.Start(); err != nil {
		return err
	}
	defer m.Close()
	refreshAutostart(env.autostart, log)

	token, err := ipc.NewToken(env.dataDir)
	if err != nil {
		return err
	}
	l, err := ipc.Listen(env.dataDir)
	if err != nil {
		return err
	}
	defer ipc.Cleanup(env.dataDir)
	srv := ipc.NewServer(m, token, log)
	go func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("ipc server stopped", "err", err)
		}
	}()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("agent started", "version", version, "dataDir", env.dataDir)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if env.tray && tray.Available() {
		return tray.Run(ctx, m, tray.Options{OnQuit: cancel, OpenGUI: guiOpener(), Log: log})
	}
	<-ctx.Done()
	log.Info("agent stopping")
	return nil
}

// logNotifier is the development notifier: it only writes the notification to the log.
type logNotifier struct{ log *slog.Logger }

func (n logNotifier) Notify(title, message string) error {
	n.log.Info("notification", "title", title, "message", message)
	return nil
}

// pickNotifier returns the log-only notifier when noNotify is set, else the desktop one.
func pickNotifier(noNotify bool, log *slog.Logger) notify.Notifier {
	if noNotify {
		return logNotifier{log: log}
	}
	return notify.NewDesktop()
}

// parseRunFlags parses the flags of "run".
func parseRunFlags(args []string) (noTray, noNotify, verbose bool, err error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	nt := fs.Bool("no-tray", false, "run without the tray icon")
	nn := fs.Bool("no-notify", false, "log notifications instead of posting desktop notifications")
	v := fs.Bool("verbose", false, "also log to stderr")
	if _, err := parseArgs(fs, args); err != nil {
		return false, false, false, err
	}
	return *nt, *nn, *v, nil
}

// runAgentCommand is "s3-anysync-agent run".
func runAgentCommand(args []string, stderr io.Writer) int {
	noTray, noNotify, verbose, err := parseRunFlags(args)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	dir, err := app.DefaultDataDir()
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	sec, err := secrets.FromEnv(dir)
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = runAgent(ctx, agentEnv{
		dataDir: dir, secrets: sec, autostart: defaultAutostart(), notifier: pickNotifier(false, nil), noNotify: noNotify,
		tray: !noTray, verbose: verbose, stderr: stderr,
	})
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	return 0
}

// guiOpener starts the GUI executable installed next to the agent (both live
// in Contents/MacOS of the .app bundle on macOS), or returns nil when there
// is none, which hides "Open" in the tray.
func guiOpener() func() error {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	name := "s3-anysync"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	gui := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(gui); err != nil {
		return nil
	}
	return func() error { return exec.Command(gui).Start() }
}

// refreshAutostart rewrites an enabled login item whose stored executable no
// longer exists (the app was moved or updated). Dev runs (own data folder via
// S3_ANYSYNC_DATA_DIR) never touch it: the entry belongs to the installed app.
func refreshAutostart(a agent.Autostart, log *slog.Logger) {
	if a == nil || os.Getenv("S3_ANYSYNC_DATA_DIR") != "" {
		return
	}
	t, ok := a.(interface{ Target() (string, error) })
	if !ok {
		return
	}
	if on, err := a.Enabled(); err != nil || !on {
		return
	}
	target, err := t.Target()
	if err != nil || target == "" {
		return
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err := a.Enable(); err != nil {
		log.Warn("could not refresh the autostart entry", "err", err)
	}
}
