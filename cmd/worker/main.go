// Command worker runs Maki's background jobs from the outbox table: image
// processing, stock alerts, daily summaries, and releasing tables at closing
// time. Job handlers are registered here as the modules that own them land.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	// Embedded zoneinfo, so outlet time zones resolve in minimal images.
	_ "time/tzdata"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/auth"
	"github.com/kervzent/maki-be/internal/order"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/payment"
	"github.com/kervzent/maki-be/internal/platform/config"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/mail"
	"github.com/kervzent/maki-be/internal/platform/outbox"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/user"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadDotEnv(); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, a second one kills the process at once instead
	// of waiting for the running jobs.
	context.AfterFunc(ctx, stop)

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// SMTP, or in development without SMTP_HOST, the log.
	mailer, err := mail.New(cfg.SMTP, cfg.IsDevelopment(), logger)
	if err != nil {
		return err
	}

	// The worker sets no passwords; one hash at a time is plenty.
	users := user.NewService(pool, password.NewHasher(password.Default, 1), audit.Recorder{}, auth.Sessions{})

	worker := outbox.NewWorker(pool, logger, outbox.Options{})
	// Each module that enqueues jobs registers their handlers here.
	outlets := outlet.NewService(pool, users, payment.NewService(pool), order.Records{}, audit.Recorder{})
	auth.NewJobs(pool, users, outlets, mailer, cfg.WebBaseURL).Register(worker)

	logger.Info("worker started", slog.String("env", cfg.Env), slog.Bool("smtp", cfg.SMTP.Host != ""))
	worker.Run(ctx)
	logger.Info("worker stopped")
	return nil
}
