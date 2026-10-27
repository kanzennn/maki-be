package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
)

// ownerCreator is the part of the auth service `api create-owner` uses.
type ownerCreator interface {
	OwnerExists(ctx context.Context) (bool, error)
	CreateOwner(ctx context.Context, in models.OwnerInput) (string, error)
}

// prompter asks questions on out and reads the answers from in. On a
// terminal, secrets are read without echo; from a pipe, as plain lines.
type prompter struct {
	in       *bufio.Reader
	out      io.Writer
	terminal bool
	fd       int
}

func stdinPrompter() *prompter {
	fd := int(os.Stdin.Fd()) //nolint:gosec // a file descriptor always fits an int
	return &prompter{in: bufio.NewReader(os.Stdin), out: os.Stdout, terminal: term.IsTerminal(fd), fd: fd}
}

// printf writes to the operator's terminal; a failed write there has nobody
// to report to.
func (p *prompter) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, format, args...)
}

func (p *prompter) line(label string) (string, error) {
	p.printf("%s", label)
	s, err := p.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		return "", fmt.Errorf("read %s: %w", strings.TrimSuffix(label, ": "), err)
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (p *prompter) secret(label string) (string, error) {
	if !p.terminal {
		return p.line(label)
	}
	p.printf("%s", label)
	b, err := term.ReadPassword(p.fd)
	p.printf("\n")
	if err != nil {
		return "", fmt.Errorf("read %s: %w", strings.TrimSuffix(label, ": "), err)
	}
	return string(b), nil
}

// createOwner runs `api create-owner`: it asks for the Owner's name,
// username, email and password — the password twice, without echo — and
// creates the account. It refuses before asking anything if the Owner
// exists. Secrets never come from flags, which end up in shell history.
func createOwner(ctx context.Context, svc ownerCreator, p *prompter) error {
	exists, err := svc.OwnerExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return apperror.ErrOwnerExists
	}

	p.printf("Create the Owner account. Password reset goes to its email, which gets a link to confirm it.\n")
	var in models.OwnerInput
	if in.Name, err = p.line("Name: "); err != nil {
		return err
	}
	if in.Username, err = p.line("Username: "); err != nil {
		return err
	}
	if in.Email, err = p.line("Email: "); err != nil {
		return err
	}
	if in.Password, err = p.secret("Password: "); err != nil {
		return err
	}
	again, err := p.secret("Repeat the password: ")
	if err != nil {
		return err
	}
	if again != in.Password {
		return errors.New("the passwords don't match; nothing was created")
	}

	if _, err := svc.CreateOwner(ctx, in); err != nil {
		// Password rules come back as API errors; say them in words.
		if coded, ok := errors.AsType[*apperror.Error](err); ok {
			return errors.New(i18n.For("en").Error(coded.Code(), coded.Details()))
		}
		return err
	}
	p.printf("Owner %s created. A link to confirm %s is on its way from the worker; until it is confirmed, password reset by email doesn't work. (In development without SMTP_HOST, the link is in the worker's log.)\n",
		strings.ToLower(strings.TrimSpace(in.Username)), strings.ToLower(strings.TrimSpace(in.Email)))
	return nil
}
