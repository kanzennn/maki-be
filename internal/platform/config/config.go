// Package config loads the configuration shared by the api and worker
// binaries from environment variables.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the typed configuration for both binaries.
type Config struct {
	Env                string
	HTTPAddr           string
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
		errs = append(errs, errors.New("DATABASE_URL is required"))
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
