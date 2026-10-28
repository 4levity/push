// Command push receives client status reports over a public HTTP port and
// serves them to authenticated consumers over a local HTTP port.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"push/internal/app"
	"push/internal/config"
	"push/internal/token"
	"syscall"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usageText = `push - client status receiver and consumer service (version %s)

Usage:
  push run       [-config /etc/push/push.yaml] [-log-format json|text]
  push gen-token
  push validate  [-config /etc/push/push.yaml]

Commands:
  run        start both HTTP services; SIGHUP reloads the config file
  gen-token  print a new random token and its sha256 hash for the config
  validate   check the config file and exit
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usageText, version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "gen-token":
		err = genToken()
	case "validate":
		err = validate(args)
	case "help", "-h", "--help":
		fmt.Printf(usageText, version)
		return
	default:
		fmt.Fprintf(os.Stderr, "push: unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usageText, version)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/push/push.yaml", "path to the config file")
	logFormat := fs.String("log-format", "json", "log format: json or text")
	// flag.ExitOnError prints usage and exits on a parse error, so Parse
	// never returns a non-nil error here.
	_ = fs.Parse(args)

	log := newLogger(*logFormat)

	a, err := app.New(*cfgPath, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-hup:
				if err := a.Reload(*cfgPath); err != nil {
					log.Error("config reload failed; keeping previous config", "err", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	cfg := a.Config()
	log.Info("push starting",
		"version", version,
		"config", *cfgPath,
		"client_listen", cfg.ClientListen,
		"consumer_listen", cfg.ConsumerListen,
		"clients", len(cfg.Clients),
		"consumers", len(cfg.Consumers))

	if err := a.Run(ctx); err != nil {
		return err
	}
	log.Info("push stopped")
	return nil
}

func genToken() error {
	tok, err := token.Generate()
	if err != nil {
		return err
	}
	fmt.Printf("token:        %s\n", tok)
	fmt.Printf("token_sha256: %s\n", token.Hash(tok))
	return nil
}

func validate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/push/push.yaml", "path to the config file")
	// flag.ExitOnError exits on a parse error, so Parse returns nil here.
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Printf("config OK: %d client(s), %d consumer(s)\n", len(cfg.Clients), len(cfg.Consumers))
	return nil
}

func newLogger(format string) *slog.Logger {
	var h slog.Handler
	switch format {
	case "text":
		h = slog.NewTextHandler(os.Stdout, nil)
	default:
		h = slog.NewJSONHandler(os.Stdout, nil)
	}
	return slog.New(h)
}
