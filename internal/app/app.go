// Package app wires configuration, storage and both HTTP services together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"push/internal/clientapi"
	"push/internal/config"
	"push/internal/consumerapi"
	"push/internal/ratelimit"
	"push/internal/store"
	"sync/atomic"
	"time"
)

const shutdownTimeout = 10 * time.Second

// App owns the live configuration, the status store and both HTTP servers.
type App struct {
	cfg             atomic.Pointer[config.Config]
	store           *store.Store
	limiter         *ratelimit.Limiter
	consumerLimiter *ratelimit.Limiter
	log             *slog.Logger
	client          *http.Server
	consumer        *http.Server
}

// New loads the config file at path and builds a ready-to-run App.
func New(path string, log *slog.Logger) (*App, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	a := &App{store: store.New(), limiter: ratelimit.New(), consumerLimiter: ratelimit.New(), log: log}
	a.cfg.Store(cfg)
	return a, nil
}

// Config returns the active configuration.
func (a *App) Config() *config.Config { return a.cfg.Load() }

// Reload re-reads the config file and atomically swaps it in on success.
// If the new file is invalid the previous configuration keeps running.
func (a *App) Reload(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("config %s is invalid: %w", path, err)
	}
	old := a.cfg.Swap(cfg)
	a.log.Info("config reloaded",
		"clients", len(cfg.Clients),
		"consumers", len(cfg.Consumers))
	for _, note := range reloadNotes(old, cfg) {
		a.log.Warn(note)
	}
	return nil
}

// reloadNotes reports changes that do not take effect until a restart.
func reloadNotes(old, next *config.Config) []string {
	var notes []string
	if old.ClientListen != next.ClientListen {
		notes = append(notes, "client_listen changed; restart push to bind the new address")
	}
	if old.ConsumerListen != next.ConsumerListen {
		notes = append(notes, "consumer_listen changed; restart push to bind the new address")
	}
	return notes
}

// Run serves both HTTP services until ctx is cancelled or a listener fails.
// Both listeners are bound before serving so a bind failure fails fast with
// nothing left running.
func (a *App) Run(ctx context.Context) error {
	cfg := a.Config()

	a.client = &http.Server{
		Handler:           clientapi.New(a, a.store, a.limiter, a.log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	a.consumer = &http.Server{
		Handler:           consumerapi.New(a, a.store, a.consumerLimiter, a.log),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	clientLn, err := net.Listen("tcp", cfg.ClientListen) //nolint:noctx // long-lived listener
	if err != nil {
		return fmt.Errorf("listen client %s: %w", cfg.ClientListen, err)
	}
	consumerLn, err := net.Listen("tcp", cfg.ConsumerListen) //nolint:noctx // long-lived listener
	if err != nil {
		_ = clientLn.Close() // best-effort cleanup; the Listen error is what we return
		return fmt.Errorf("listen consumer %s: %w", cfg.ConsumerListen, err)
	}

	a.log.Info("client service listening", "addr", cfg.ClientListen)
	a.log.Info("consumer service listening", "addr", cfg.ConsumerListen)

	errCh := make(chan error, 2)
	go func() {
		errCh <- a.client.Serve(clientLn)
	}()
	go func() {
		errCh <- a.consumer.Serve(consumerLn)
	}()

	sweeperStop := make(chan struct{})
	defer close(sweeperStop)
	go a.limiter.StartSweeper(time.Minute, func() time.Duration {
		return a.Config().RateLimit.Window
	}, sweeperStop)
	// The consumer limit is a fixed constant, not a config value.
	go a.consumerLimiter.StartSweeper(time.Minute, func() time.Duration {
		return consumerapi.LimitWindow
	}, sweeperStop)

	select {
	case err := <-errCh:
		a.shutdown()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		a.shutdown()
		return nil
	}
}

func (a *App) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	a.log.Info("shutting down")
	_ = a.consumer.Shutdown(ctx)
	_ = a.client.Shutdown(ctx)
}
