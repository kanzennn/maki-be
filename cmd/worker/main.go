// Command worker runs Maki's background jobs from the outbox table: image
// processing, stock alerts, daily summaries, and releasing tables at closing
// time. Each kind of job (internal/jobs) registers its handlers here as it
// lands.
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

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/jobs"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/mail"
	"github.com/kervzent/maki-be/internal/utils/outbox"
	"github.com/kervzent/maki-be/internal/utils/password"
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

	pool, err := config.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// SMTP, or in development without SMTP_HOST, the log.
	mailer, err := mail.New(cfg.SMTP, cfg.IsDevelopment(), logger)
	if err != nil {
		return err
	}

	db := config.NewDatabase(pool)
	auditor := services.NewAuditService(repositories.NewAuditRepository(db))
	// The worker sets no passwords; one hash at a time is plenty.
	users := services.NewUserService(repositories.NewUserRepository(db), password.NewHasher(password.Default, 1))
	roles := services.NewRoleService(db, repositories.NewRoleRepository(db), auditor)

	// The outlets give each email its recipient's language.
	outlets := services.NewOutletService(db, repositories.NewOutletRepository(db), users, roles,
		services.NewPaymentService(repositories.NewPaymentRepository(db)), repositories.NewOrderRepository(db), auditor)

	worker := outbox.NewWorker(pool, logger, outbox.Options{})
	// Every kind of job registers its handlers here.
	jobs.NewAuthJobs(users, outlets, services.NewAuthTokenService(repositories.NewAuthTokenRepository(db)),
		mailer, cfg.WebBaseURL).Register(worker)

	logger.Info("worker started", slog.String("env", cfg.Env), slog.Bool("smtp", cfg.SMTP.Host != ""))
	worker.Run(ctx)
	logger.Info("worker stopped")
	return nil
}
