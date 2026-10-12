package config

import (
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// setEnv sets every variable config reads to its value in vars, or clears
// it. JWT_SECRET and WEB_BASE_URL get valid test values unless vars names
// them.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	defaults := map[string]string{"JWT_SECRET": testSecret, "WEB_BASE_URL": "https://admin.maki.test"}
	for _, key := range []string{
		"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "LOG_LEVEL", "CORS_ALLOWED_ORIGINS", "TRUSTED_PROXIES",
		"DB_HOST", "DB_PORT", "DB_DATABASE", "DB_USERNAME", "DB_PASSWORD", "DB_SSLMODE",
		"JWT_SECRET", "WEB_BASE_URL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_TLS", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
	} {
		v, ok := vars[key]
		if !ok {
			v = defaults[key]
		}
		t.Setenv(key, v)
	}
}

func TestLoadWebBaseURL(t *testing.T) {
	setEnv(t, map[string]string{"APP_ENV": "development", "DATABASE_URL": "postgres://localhost/maki", "WEB_BASE_URL": ""})
	if cfg, err := Load(); err != nil || cfg.WebBaseURL != "http://localhost:3002" {
		t.Errorf("development without WEB_BASE_URL = %q, %v; want the dev server", cfg.WebBaseURL, err)
	}
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki", "WEB_BASE_URL": ""})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WEB_BASE_URL") {
		t.Errorf("production without WEB_BASE_URL: error = %v, want one naming it", err)
	}
	for _, bad := range []string{"admin.maki.test", "https://admin.maki.test/", "ftp://admin.maki.test"} {
		setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki", "WEB_BASE_URL": bad})
		if _, err := Load(); err == nil {
			t.Errorf("WEB_BASE_URL=%q accepted", bad)
		}
	}
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki"})
	if cfg, err := Load(); err != nil || cfg.WebBaseURL != "https://admin.maki.test" {
		t.Errorf("WebBaseURL = %q, %v", cfg.WebBaseURL, err)
	}
}

func TestLoadRequiresAStrongJWTSecret(t *testing.T) {
	for _, secret := range []string{"", "too-short", testSecret[:31]} {
		setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki", "JWT_SECRET": secret})
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
			t.Errorf("JWT_SECRET of %d bytes: Load() error = %v, want one naming JWT_SECRET", len(secret), err)
		}
	}
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki"})
	cfg, err := Load()
	if err != nil || cfg.JWTSecret != testSecret {
		t.Errorf("Load() = %q, %v; want the 32-byte secret", cfg.JWTSecret, err)
	}
}

func TestLoadValid(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":              "development",
		"DATABASE_URL":         "postgres://localhost:5432/maki",
		"LOG_LEVEL":            "debug",
		"CORS_ALLOWED_ORIGINS": "http://localhost:3002, https://admin.example.com",
		"TRUSTED_PROXIES":      "10.0.0.1,10.0.1.0/24",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.IsDevelopment() {
		t.Errorf("IsDevelopment() = false, want true")
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want default :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if want := []string{"http://localhost:3002", "https://admin.example.com"}; !slices.Equal(cfg.CORSAllowedOrigins, want) {
		t.Errorf("CORSAllowedOrigins = %v, want %v", cfg.CORSAllowedOrigins, want)
	}
	if want := []string{"10.0.0.1", "10.0.1.0/24"}; !slices.Equal(cfg.TrustedProxies, want) {
		t.Errorf("TrustedProxies = %v, want %v", cfg.TrustedProxies, want)
	}
}

func TestLoadDefaultsToProduction(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.IsDevelopment() {
		t.Errorf("an unset APP_ENV must not mean development")
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":              "staging",
		"LOG_LEVEL":            "loud",
		"CORS_ALLOWED_ORIGINS": "*",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "LOG_LEVEL", "CORS_ALLOWED_ORIGINS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// A shared PostgreSQL server's env file, as production gets it: pgx must
// read back exactly the parts, the password's every character included.
func TestLoadDatabaseFromParts(t *testing.T) {
	const password = `p@ss:w/rd?#%&+ é"`
	setEnv(t, map[string]string{
		"DB_HOST":     "postgresql-18",
		"DB_PORT":     "5433",
		"DB_DATABASE": "maki",
		"DB_USERNAME": "maki",
		"DB_PASSWORD": password,
		"DB_SSLMODE":  "disable",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	pg, err := pgconn.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("pgx can't read the URL: %v", err)
	}
	if pg.Host != "postgresql-18" || pg.Port != 5433 || pg.Database != "maki" || pg.User != "maki" {
		t.Errorf("pgx reads %s:%d, database %q, user %q", pg.Host, pg.Port, pg.Database, pg.User)
	}
	if pg.Password != password {
		t.Errorf("pgx reads a different password from %q", cfg.DatabaseURL)
	}
	if pg.TLSConfig != nil {
		t.Errorf("DB_SSLMODE=disable, yet pgx would use TLS")
	}
}

func TestLoadDatabaseFromPartsDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DB_HOST": "db", "DB_DATABASE": "maki", "DB_USERNAME": "maki"})

	cfg, err := Load()
	if err != nil || cfg.DatabaseURL != "postgres://maki@db:5432/maki" {
		t.Errorf("Load() = %q, %v; want port 5432, no password and pgx's own sslmode", cfg.DatabaseURL, err)
	}
}

func TestLoadDatabaseURLWinsOverParts(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/maki",
		"DB_HOST":      "db",
		"DB_DATABASE":  "other",
		"DB_USERNAME":  "other",
	})

	cfg, err := Load()
	if err != nil || cfg.DatabaseURL != "postgres://localhost/maki" {
		t.Errorf("Load() = %q, %v; want DATABASE_URL as it is", cfg.DatabaseURL, err)
	}
}

func TestLoadRejectsBadDatabaseParts(t *testing.T) {
	whole := map[string]string{"DB_HOST": "db", "DB_DATABASE": "maki", "DB_USERNAME": "maki"}
	with := func(key, value string) map[string]string {
		vars := maps.Clone(whole)
		vars[key] = value
		return vars
	}
	for _, tc := range []struct {
		name string
		vars map[string]string
		want []string
	}{
		{"host alone", map[string]string{"DB_HOST": "db", "DB_PASSWORD": "secret"}, []string{"DB_DATABASE is required", "DB_USERNAME is required"}},
		{"no host", with("DB_HOST", ""), []string{"DB_HOST is required"}},
		{"port", with("DB_PORT", "fifty"), []string{"DB_PORT"}},
		{"port zero", with("DB_PORT", "0"), []string{"DB_PORT"}},
		{"port out of range", with("DB_PORT", "65536"), []string{"DB_PORT"}},
		{"sslmode", with("DB_SSLMODE", "on"), []string{"DB_SSLMODE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.vars)
			_, err := Load()
			for _, want := range tc.want {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("Load() error = %v, want one naming %s", err, want)
				}
			}
		})
	}
}

func TestLoadRejectsInexactOrigins(t *testing.T) {
	for _, origin := range []string{"*", "https://*.example.com", "admin.example.com", "https://admin.example.com/", "https://admin.example.com/path"} {
		t.Run(origin, func(t *testing.T) {
			setEnv(t, map[string]string{
				"DATABASE_URL":         "postgres://localhost/maki",
				"CORS_ALLOWED_ORIGINS": origin,
			})
			if _, err := Load(); err == nil {
				t.Errorf("Load() accepted CORS origin %q", origin)
			}
		})
	}
}

func TestLoadSMTP(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":  "postgres://localhost/maki",
		"SMTP_HOST":     "smtp.example.com",
		"SMTP_USERNAME": "maki",
		"SMTP_PASSWORD": "secret",
		"SMTP_FROM":     "Maki <no-reply@example.com>",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := SMTP{Host: "smtp.example.com", Port: 587, TLS: SMTPStartTLS, Username: "maki", Password: "secret", From: "Maki <no-reply@example.com>"}
	if cfg.SMTP != want {
		t.Errorf("SMTP = %+v, want %+v", cfg.SMTP, want)
	}
}

func TestLoadSMTPImplicitTLSDefaultsToPort465(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://localhost/maki",
		"SMTP_HOST":    "smtp.example.com",
		"SMTP_TLS":     "tls",
		"SMTP_FROM":    "no-reply@example.com",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SMTP.Port != 465 {
		t.Errorf("SMTP.Port = %d, want 465", cfg.SMTP.Port)
	}
}

func TestLoadSMTPWithoutHostIsFine(t *testing.T) {
	// The api never sends email; the worker decides whether a missing host
	// is acceptable.
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://localhost/maki"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SMTP.Host != "" || cfg.SMTP.TLS != SMTPStartTLS {
		t.Errorf("SMTP = %+v, want no host and starttls", cfg.SMTP)
	}
}

func TestLoadRejectsBadSMTP(t *testing.T) {
	for name, tc := range map[string]struct {
		vars map[string]string
		want string
	}{
		"plain text in production":  {map[string]string{"SMTP_TLS": "none"}, "SMTP_TLS=none"},
		"unknown TLS mode":          {map[string]string{"SMTP_TLS": "ssl"}, "SMTP_TLS"},
		"port is not a number":      {map[string]string{"SMTP_PORT": "smtp"}, "SMTP_PORT"},
		"port out of range":         {map[string]string{"SMTP_PORT": "70000"}, "SMTP_PORT"},
		"username without password": {map[string]string{"SMTP_USERNAME": "maki"}, "SMTP_PASSWORD"},
		"sender is not an address":  {map[string]string{"SMTP_FROM": "Maki"}, "SMTP_FROM"},
		"host without sender":       {map[string]string{"SMTP_HOST": "smtp.example.com"}, "SMTP_FROM"},
	} {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"DATABASE_URL": "postgres://localhost/maki"}
			maps.Copy(vars, tc.vars)
			setEnv(t, vars)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Load() error = %v, want one mentioning %s", err, tc.want)
			}
		})
	}
}

func TestLoadAllowsPlainSMTPInDevelopment(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":      "development",
		"DATABASE_URL": "postgres://localhost/maki",
		"SMTP_HOST":    "localhost",
		"SMTP_PORT":    "1025",
		"SMTP_TLS":     "none",
		"SMTP_FROM":    "Maki <no-reply@maki.local>",
	})

	if _, err := Load(); err != nil {
		t.Errorf("Load() error = %v, want none: a local mail catcher speaks plain SMTP", err)
	}
}
