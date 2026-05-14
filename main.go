package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/proxy"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/ui"
)

var Version = "dev"

const (
	exitUsage = 2
	exitRun   = 1
)

func main() {
	if Version == "dev" || Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			Version = bi.Main.Version
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if errors.Is(err, config.ErrUsage) {
			os.Exit(exitUsage)
		}
		os.Exit(exitRun)
	}
}

func run() error {
	flags := config.Flags{}
	var showVersion bool
	var showHelp bool

	flag.StringVar(&flags.ConfigPath, "config", "", "Path to YAML config file (env: LOCALMODELPROXY_CONFIG)")
	flag.StringVar(&flags.Host, "host", "", "Local bind host (default: 127.0.0.1)")
	flag.IntVar(&flags.Port, "port", 0, "Local bind port (default: 8080)")
	flag.StringVar(&flags.UIMode, "ui", "", "UI mode: auto, tui, plain, jsonl")
	flag.BoolVar(&flags.Verbose, "verbose", false, "Enable verbose diagnostics")
	flag.BoolVar(&showVersion, "version", false, "Show version")
	flag.BoolVar(&showHelp, "help", false, "Show help")
	flag.Parse()

	if showVersion {
		fmt.Fprintf(os.Stderr, "localmodelproxy version %s\n", Version)
		return nil
	}
	if showHelp {
		printHelp()
		return nil
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	metrics := proxy.NewMetrics()
	handler, err := proxy.New(ctx, proxy.Options{
		Config:    cfg,
		Metrics:   metrics,
		Verbose:   cfg.Verbose,
		LogOutput: os.Stderr,
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Address(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		done <- server.ListenAndServe()
	}()

	renderer := ui.Start(ctx, cancel, cfg, metrics, os.Stdout, os.Stderr)
	defer renderer.Stop()

	select {
	case <-ctx.Done():
		if err := shutdownServer(server, done); err != nil {
			return err
		}
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	renderer.FinalSummary()
	return nil
}

func shutdownServer(server *http.Server, done <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			return fmt.Errorf("graceful shutdown failed: %w; forced close failed: %v", err, closeErr)
		}
	}

	err := <-done
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func printHelp() {
	fmt.Fprintf(os.Stderr, `localmodelproxy - Local OpenAI-compatible proxy

Usage:
  localmodelproxy [OPTIONS]

Options:
  --config PATH       Path to YAML config file (env: LOCALMODELPROXY_CONFIG)
  --host HOST         Local bind host (default: 127.0.0.1)
  --port PORT         Local bind port (default: 8080)
  --ui MODE           UI mode: auto, tui, plain, jsonl
  --verbose           Log each request to console with token info; disables TUI
  --version           Print version and exit
  --help              Print help and exit

Config defaults to ~/.localmodelproxy when present.
Authenticate with: gcloud auth application-default login
Point OpenAI-compatible clients at: http://127.0.0.1:8080/v1
`)
}
