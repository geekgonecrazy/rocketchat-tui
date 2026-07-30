// Command rctui is a terminal client for Rocket.Chat.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/geekgonecrazy/rocketchat-tui/internal/config"
	"github.com/geekgonecrazy/rocketchat-tui/internal/store"
	"github.com/geekgonecrazy/rocketchat-tui/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "rctui: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		serverFlag = flag.String("server", "", "Rocket.Chat server URL (overrides the saved one)")
		dbFlag     = flag.String("db", "", "path to the local cache database")
		logFlag    = flag.String("log", "", "write debug logs to this file")
		logoutFlag = flag.Bool("logout", false, "forget the saved credentials and exit")
	)
	flag.Parse()

	configPath, dbPath, err := config.Paths()
	if err != nil {
		return err
	}
	if *dbFlag != "" {
		dbPath = *dbFlag
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	if *logoutFlag {
		cfg.ClearSession()
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("Saved credentials removed.")
		return nil
	}

	logger, closeLog, err := newLogger(*logFlag)
	if err != nil {
		return err
	}
	defer closeLog()

	cache, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer cache.Close()

	// Cancel on SIGINT/SIGTERM so the realtime connection and the core shut down
	// cleanly even if the TUI is killed from outside.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	program := tea.NewProgram(
		ui.NewRoot(ctx, cfg, cache, logger, *serverFlag),
		tea.WithAltScreen(),
		// Cell motion rather than all motion: we only need clicks and the wheel,
		// and this leaves text selection working with shift held down in most
		// terminals.
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

// newLogger writes structured logs to a file when requested. Logging to stdout
// would corrupt the TUI, so the default is to discard.
func newLogger(path string) (*slog.Logger, func(), error) {
	if path == "" {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %s: %w", path, err)
	}
	logger := slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, func() { _ = file.Close() }, nil
}
