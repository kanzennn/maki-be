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

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/routes"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/jwt"
	"github.com/kervzent/maki-be/internal/utils/password"
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

	pool, err := config.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch command {
	case "migrate":
		return config.Migrate(ctx, pool, logger)
	case "create-owner":
		svc, err := newApp(cfg, logger, pool)
		if err != nil {
			return err
		}
		return createOwner(ctx, svc.auth, stdinPrompter())
	}
	return serve(ctx, cfg, logger, pool)
}

// app is every service, each built once and handed to the services and
// controllers that depend on it.
type app struct {
	tokens     *jwt.Tokens
	roles      *services.RoleService
	staff      *services.StaffService
	outlets    *services.OutletService
	categories *services.CategoryService
	menus      *services.MenuService
	stock      *services.StockService
	devices    *services.DeviceService
	areas      *services.AreaService
	layouts    *services.LayoutService
	tables     *services.TableService
	schedules  *services.ScheduleService
	modes      *services.DineInModeService
	vouchers   *services.VoucherService
	pos        *services.PosService
	checkout   *services.CheckoutService
	orders     *services.OrderService
	auth       *services.AuthService
}

func newApp(cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) (app, error) {
	tokens, err := jwt.NewTokens([]byte(cfg.JWTSecret), services.AccessTokenTTL)
	if err != nil {
		return app{}, err
	}
	// At most 4 argon2id hashes at once: 4 × 64 MiB during a burst of
	// sign-ins, however many arrive.
	hasher := password.NewHasher(password.Default, 4)
	db := config.NewDatabase(pool)
	auditor := services.NewAuditService(repositories.NewAuditRepository(db))
	userRepo, roleRepo := repositories.NewUserRepository(db), repositories.NewRoleRepository(db)
	sessions := repositories.NewSessionRepository(db)
	users := services.NewUserService(userRepo, hasher)
	roles := services.NewRoleService(db, roleRepo, auditor)
	payments := services.NewPaymentService(repositories.NewPaymentRepository(db))
	orderRepo := repositories.NewOrderRepository(db)
	outlets := services.NewOutletService(db, repositories.NewOutletRepository(db), users, roles, payments, orderRepo, auditor)
	menuRepo, categoryRepo, stockRepo := repositories.NewMenuRepository(db), repositories.NewCategoryRepository(db), repositories.NewStockRepository(db)
	menus := services.NewMenuService(db, menuRepo, categoryRepo, stockRepo, auditor)
	devices := services.NewDeviceService(db, repositories.NewDeviceRepository(db), users, auditor)
	areaRepo, tableRepo := repositories.NewAreaRepository(db), repositories.NewTableRepository(db)
	modes := services.NewDineInModeService(db, repositories.NewDineInModeRepository(db), auditor)
	tables := services.NewTableService(db, tableRepo, areaRepo, modes, outlets, orderRepo, auditor, time.Now)
	vouchers := services.NewVoucherService(db, repositories.NewVoucherRepository(db), outlets, devices, orderRepo, auditor, time.Now)
	orders := services.NewOrderService(db, orderRepo, menus, outlets, users, vouchers, auditor, time.Now)
	return app{
		tokens:     tokens,
		roles:      roles,
		staff:      services.NewStaffService(db, userRepo, roleRepo, hasher, auditor, sessions),
		outlets:    outlets,
		categories: services.NewCategoryService(db, categoryRepo, auditor),
		menus:      menus,
		stock:      services.NewStockService(db, menuRepo, stockRepo, users, auditor),
		devices:    devices,
		areas:      services.NewAreaService(db, areaRepo, auditor),
		layouts:    services.NewLayoutService(db, areaRepo, tableRepo, tables, auditor),
		tables:     tables,
		schedules:  services.NewScheduleService(db, repositories.NewScheduleRepository(db), tableRepo, users, auditor, time.Now),
		modes:      modes,
		vouchers:   vouchers,
		pos:        services.NewPosService(orderRepo, menus, devices, payments, outlets, modes, time.Now),
		checkout: services.NewCheckoutService(db, orderRepo, orders, menus, devices, payments, outlets, vouchers, tables,
			modes, auditor, time.Now),
		orders: orders,
		auth: services.NewAuthService(db, sessions, services.NewAuthTokenService(repositories.NewAuthTokenRepository(db)),
			users, outlets, tokens, hasher, auditor),
	}, nil
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) error {
	svc, err := newApp(cfg, logger, pool)
	if err != nil {
		return err
	}

	router, err := routes.New(cfg, logger, routes.Deps{
		Pinger:     pool,
		Tokens:     svc.tokens,
		Principals: svc.auth,
		// Plain-HTTP development can't set a Secure cookie on a LAN address.
		Auth:         controllers.NewAuthController(svc.auth, svc.tokens, !cfg.IsDevelopment()),
		OutletFinder: svc.outlets,
		Outlets:      controllers.NewOutletController(svc.outlets),
		Roles:        controllers.NewRoleController(svc.roles),
		Staff:        controllers.NewStaffController(svc.staff),
		Categories:   controllers.NewCategoryController(svc.categories),
		Menus:        controllers.NewMenuController(svc.menus),
		Stock:        controllers.NewStockController(svc.stock),
		Devices:      controllers.NewDeviceController(svc.devices, !cfg.IsDevelopment()),
		Tables: routes.TableControllers{
			Areas:     controllers.NewAreaController(svc.areas),
			Layouts:   controllers.NewLayoutController(svc.layouts),
			Tables:    controllers.NewTableController(svc.tables),
			Schedules: controllers.NewScheduleController(svc.schedules),
			Modes:     controllers.NewDineInModeController(svc.modes),
		},
		Vouchers: controllers.NewVoucherController(svc.vouchers),
		Orders: routes.OrderControllers{
			Pos:      controllers.NewPosController(svc.pos, !cfg.IsDevelopment()),
			Checkout: controllers.NewCheckoutController(svc.checkout),
			Orders:   controllers.NewOrderController(svc.orders),
		},
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
