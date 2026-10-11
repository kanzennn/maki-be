// Command api serves the Maki HTTP API.
//
// Usage:
//
//	api                serve the API
//	api migrate        apply pending database migrations, then exit
//	api create-owner   create the one Owner account, asking for its details
//
// Deploys run `api migrate` before starting the new version; the migrations
// are embedded in the binary, so it needs no goose or source tree. An
// operator runs `api create-owner` once, on the server, after the first
// deploy: there is no sign-up page anyone could reach first.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// Embedded zoneinfo, so outlet time zones resolve in minimal images.
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/auth"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/payment"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/config"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/table"
	"github.com/kervzent/maki-be/internal/user"
	"github.com/kervzent/maki-be/internal/voucher"
)

const usage = `usage:
  api                serve the API
  api migrate        apply pending database migrations, then exit
  api create-owner   create the one Owner account, asking for its details`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := strings.Join(args, " ")
	switch command {
	case "", "migrate", "create-owner":
	default:
		return fmt.Errorf("unknown command %q\n%s", command, usage)
	}

	if err := config.LoadDotEnv(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if !cfg.IsDevelopment() {
		gin.SetMode(gin.ReleaseMode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, a second one kills the process at once.
	context.AfterFunc(ctx, stop)

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch command {
	case "migrate":
		return db.Migrate(ctx, pool, logger)
	case "create-owner":
		svc, err := newServices(cfg, logger, pool)
		if err != nil {
			return err
		}
		return createOwner(ctx, svc.auth, stdinPrompter())
	}
	return serve(ctx, cfg, logger, pool)
}

// services are the modules' services, each built once and handed to the
// modules that depend on it.
type services struct {
	tokens   *authn.Tokens
	users    *user.Service
	outlets  *outlet.Service
	menus    *menu.Service
	devices  *device.Service
	tables   *table.Service
	vouchers *voucher.Service
	orders   *order.Service
	auth     *auth.Service
}

func newServices(cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) (services, error) {
	tokens, err := authn.NewTokens([]byte(cfg.JWTSecret), auth.AccessTokenTTL)
	if err != nil {
		return services{}, err
	}
	// At most 4 argon2id hashes at once: 4 × 64 MiB during a burst of
	// sign-ins, however many arrive.
	hasher := password.NewHasher(password.Default, 4)
	users := user.NewService(pool, hasher, audit.Recorder{}, auth.Sessions{})
	payments := payment.NewService(pool)
	outlets := outlet.NewService(pool, users, payments, order.Records{}, audit.Recorder{})
	menus := menu.NewService(pool, users, audit.Recorder{})
	devices := device.NewService(pool, users, audit.Recorder{})
	tables := table.NewService(pool, outlets, order.Records{}, users, audit.Recorder{})
	vouchers := voucher.NewService(pool, outlets, devices, order.Records{}, audit.Recorder{})
	return services{
		tokens:   tokens,
		users:    users,
		outlets:  outlets,
		menus:    menus,
		devices:  devices,
		tables:   tables,
		vouchers: vouchers,
		orders:   order.NewService(pool, menus, devices, payments, outlets, users, vouchers, tables, audit.Recorder{}),
		auth:     auth.NewService(pool, users, outlets, tokens, hasher, audit.Recorder{}, logger),
	}, nil
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) error {
	svc, err := newServices(cfg, logger, pool)
	if err != nil {
		return err
	}

	router, err := newRouter(cfg, logger, routerDeps{
		pinger:     pool,
		tokens:     svc.tokens,
		principals: svc.auth,
		// Plain-HTTP development can't set a Secure cookie on a LAN address.
		auth:         auth.NewHandler(svc.auth, svc.tokens, logger, !cfg.IsDevelopment()),
		outletFinder: svc.outlets,
		outlets:      outlet.NewHandler(svc.outlets, logger),
		users:        user.NewHandler(svc.users, logger),
		menus:        menu.NewHandler(svc.menus, logger),
		devices:      device.NewHandler(svc.devices, logger, !cfg.IsDevelopment()),
		tables:       table.NewHandler(svc.tables, logger),
		vouchers:     voucher.NewHandler(svc.vouchers, logger),
		orders:       order.NewHandler(svc.orders, logger, !cfg.IsDevelopment()),
	})
	if err != nil {
		return err
	}

	// Explicit timeouts: the zero values mean "no timeout" and leave the
	// server open to slow clients holding connections forever.
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("api listening", slog.String("addr", cfg.HTTPAddr), slog.String("env", cfg.Env))
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("api shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
