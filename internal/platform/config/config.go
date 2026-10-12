// Package config loads the configuration shared by the api and worker
// binaries from environment variables.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the typed configuration for both binaries.
type Config struct {
	Env      string
	HTTPAddr string
	// DatabaseURL is DATABASE_URL, or else the URL the DB_ variables make
	// (databaseURLFromParts).
	DatabaseURL        string
	LogLevel           slog.Level
	CORSAllowedOrigins []string
	TrustedProxies     []string
	// JWTSecret signs access tokens; at least 32 bytes.
	JWTSecret string
	// WebBaseURL is the web app's origin, for the links in emails, such as
	// https://admin.example.com.
	WebBaseURL string
	SMTP       SMTP
}

// minJWTSecret matches authn.MinSecretLength; config can't import authn,
// which would make every package that reads config depend on JWT code.
const minJWTSecret = 32

// SMTP is the mail server the worker sends email through. Without a Host
// the worker writes each email to its log instead, which only development
// allows (the worker refuses to start otherwise).
type SMTP struct {
	Host string
	Port int
	// TLS is "starttls" (the default, port 587), "tls" (implicit TLS, port
	// 465), or "none", which development allows for a local mail catcher.
	TLS      string
	Username string // empty: the server needs no authentication
	Password string
	From     string // sender, as "Name <address>"
}

// SMTP TLS modes.
const (
	SMTPStartTLS = "starttls"
	SMTPTLS      = "tls"
	SMTPNoTLS    = "none"
)

// IsDevelopment reports whether the process runs in local development.
func (c Config) IsDevelopment() bool { return c.Env == "development" }

// LoadDotEnv loads a local .env file into the environment. It is a
// development convenience: it never overrides variables that are already set,
// and a missing file is not an error because production images don't ship one.
func LoadDotEnv() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	return nil
}

// Load reads the configuration from the environment. Every missing or
// malformed variable is reported at once, so a misconfigured deploy fails
// with one message that names all of them.
func Load() (Config, error) {
	var errs []error

	cfg := Config{
		Env:                getOr("APP_ENV", "production"),
		HTTPAddr:           getOr("HTTP_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		CORSAllowedOrigins: list(os.Getenv("CORS_ALLOWED_ORIGINS")),
		TrustedProxies:     list(os.Getenv("TRUSTED_PROXIES")),
		JWTSecret:          os.Getenv("JWT_SECRET"),
	}

	if cfg.Env != "development" && cfg.Env != "production" {
		errs = append(errs, fmt.Errorf("APP_ENV must be development or production, got %q", cfg.Env))
	}
	if cfg.DatabaseURL == "" {
		var dbErrs []error
		cfg.DatabaseURL, dbErrs = databaseURLFromParts()
		errs = append(errs, dbErrs...)
		if cfg.DatabaseURL == "" && len(dbErrs) == 0 {
			errs = append(errs, errors.New("DATABASE_URL, or DB_HOST, DB_DATABASE and DB_USERNAME, is required"))
		}
	}
	if len(cfg.JWTSecret) < minJWTSecret {
		errs = append(errs, fmt.Errorf("JWT_SECRET is required and must be at least %d bytes; generate one with: openssl rand -base64 48", minJWTSecret))
	}
	cfg.WebBaseURL = os.Getenv("WEB_BASE_URL")
	switch {
	case cfg.WebBaseURL == "" && cfg.Env == "development":
		cfg.WebBaseURL = "http://localhost:3002" // maki-fe's dev server
	case cfg.WebBaseURL == "":
		errs = append(errs, errors.New("WEB_BASE_URL is required outside development: emails link to it"))
	default:
		if err := validateOrigin(cfg.WebBaseURL); err != nil {
			errs = append(errs, fmt.Errorf("WEB_BASE_URL: %w", err))
		}
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(getOr("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	for _, origin := range cfg.CORSAllowedOrigins {
		if err := validateOrigin(origin); err != nil {
			errs = append(errs, fmt.Errorf("CORS_ALLOWED_ORIGINS: %w", err))
		}
	}
	var smtpErrs []error
	cfg.SMTP, smtpErrs = loadSMTP(cfg.IsDevelopment())
	errs = append(errs, smtpErrs...)

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}

// sslModes are the sslmode values libpq, and so pgx, takes.
var sslModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// databaseURLFromParts makes the database's URL from the variables a shared
// PostgreSQL server hands each of its apps in an env file, by Laravel's
// names: DB_HOST, DB_PORT (5432 when empty), DB_DATABASE, DB_USERNAME,
// DB_PASSWORD, and DB_SSLMODE (pgx's own default, prefer, when empty). Each
// part is escaped in the URL, so a password needs no care. It returns ""
// when none of host, database and user is set.
func databaseURLFromParts() (string, []error) {
	host, name, user := os.Getenv("DB_HOST"), os.Getenv("DB_DATABASE"), os.Getenv("DB_USERNAME")
	port, password, sslmode := getOr("DB_PORT", "5432"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_SSLMODE")
	if host == "" && name == "" && user == "" {
		return "", nil
	}

	var errs []error
	for _, part := range [][2]string{{"DB_HOST", host}, {"DB_DATABASE", name}, {"DB_USERNAME", user}} {
		if part[1] == "" {
			errs = append(errs, fmt.Errorf("%s is required with the other DB_ variables, when DATABASE_URL isn't set", part[0]))
		}
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		errs = append(errs, fmt.Errorf("DB_PORT must be a port number, got %q", port))
	}
	if sslmode != "" && !slices.Contains(sslModes, sslmode) {
		errs = append(errs, fmt.Errorf("DB_SSLMODE must be one of %s, got %q", strings.Join(sslModes, ", "), sslmode))
	}
	if len(errs) > 0 {
		return "", errs
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + name,
	}
	if password == "" {
		u.User = url.User(user)
	}
	if sslmode != "" {
		u.RawQuery = url.Values{"sslmode": {sslmode}}.Encode()
	}
	return u.String(), nil
}

func loadSMTP(development bool) (SMTP, []error) {
	var errs []error
	s := SMTP{
		Host:     os.Getenv("SMTP_HOST"),
		TLS:      getOr("SMTP_TLS", SMTPStartTLS),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     os.Getenv("SMTP_FROM"),
	}

	switch s.TLS {
	case SMTPStartTLS, SMTPTLS:
	case SMTPNoTLS:
		if !development {
			errs = append(errs, errors.New("SMTP_TLS=none is only allowed in development: it sends passwords and email in plain text"))
		}
	default:
		errs = append(errs, fmt.Errorf("SMTP_TLS must be starttls, tls or none, got %q", s.TLS))
	}

	defaultPort := "587"
	if s.TLS == SMTPTLS {
		defaultPort = "465"
	}
	port, err := strconv.Atoi(getOr("SMTP_PORT", defaultPort))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, fmt.Errorf("SMTP_PORT must be a port number, got %q", os.Getenv("SMTP_PORT")))
	}
	s.Port = port

	if (s.Username == "") != (s.Password == "") {
		errs = append(errs, errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together"))
	}
	if s.From != "" {
		if _, err := mail.ParseAddress(s.From); err != nil {
			errs = append(errs, fmt.Errorf("SMTP_FROM must be an address like \"Maki <no-reply@example.com>\", got %q", s.From))
		}
	} else if s.Host != "" {
		errs = append(errs, errors.New("SMTP_FROM is required when SMTP_HOST is set"))
	}
	return s, errs
}

// validateOrigin accepts only exact origins such as https://admin.example.com.
// A wildcard would let any site call the API with the user's credentials.
func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Host, "*") {
		return fmt.Errorf("%q is not an exact origin like https://admin.example.com", origin)
	}
	return nil
}

func getOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// list splits a comma-separated variable, trimming spaces and dropping empty
// entries.
func list(v string) []string {
	var out []string
	for item := range strings.SplitSeq(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
