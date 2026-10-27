package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/password"
)

type fakeOwners struct {
	exists  bool
	err     error
	created *models.OwnerInput
}

func (f *fakeOwners) OwnerExists(context.Context) (bool, error) { return f.exists, nil }

func (f *fakeOwners) CreateOwner(_ context.Context, in models.OwnerInput) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.created = &in
	return "u1", nil
}

// pipe returns a prompter reading answers from input, as from a pipe rather
// than a terminal.
func pipe(input string) (*prompter, *bytes.Buffer) {
	var out bytes.Buffer
	return &prompter{in: bufio.NewReader(strings.NewReader(input)), out: &out}, &out
}

func TestCreateOwnerAsksAndCreates(t *testing.T) {
	owners := &fakeOwners{}
	p, out := pipe("Faiz\npemilik\nowner@maki.test\nsandi owner panjang\nsandi owner panjang\n")

	if err := createOwner(t.Context(), owners, p); err != nil {
		t.Fatalf("createOwner: %v", err)
	}
	want := models.OwnerInput{Name: "Faiz", Username: "pemilik", Email: "owner@maki.test", Password: "sandi owner panjang"}
	if owners.created == nil || *owners.created != want {
		t.Errorf("created %+v, want %+v", owners.created, want)
	}
	for _, s := range []string{"Name: ", "Username: ", "Email: ", "Password: ", "Repeat the password: ", "Owner pemilik created"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(out.String(), "sandi owner panjang") {
		t.Error("the password was echoed")
	}
}

func TestCreateOwnerRefusesWhenOneExists(t *testing.T) {
	p, out := pipe("")
	err := createOwner(t.Context(), &fakeOwners{exists: true}, p)
	if !errors.Is(err, apperror.ErrOwnerExists) || out.Len() != 0 {
		t.Errorf("createOwner = %v with output %q; want ErrOwnerExists before any question", err, out)
	}
}

func TestCreateOwnerNeedsMatchingPasswords(t *testing.T) {
	owners := &fakeOwners{}
	p, _ := pipe("Faiz\npemilik\nowner@maki.test\nsandi owner panjang\nsandi lain\n")
	if err := createOwner(t.Context(), owners, p); err == nil || !strings.Contains(err.Error(), "don't match") || owners.created != nil {
		t.Errorf("createOwner = %v, created %v; want a mismatch error and nothing created", err, owners.created)
	}
}

func TestCreateOwnerExplainsPasswordRules(t *testing.T) {
	owners := &fakeOwners{err: password.Check("pendek", "pemilik")}
	p, _ := pipe("Faiz\npemilik\nowner@maki.test\npendek\npendek\n")
	err := createOwner(t.Context(), owners, p)
	if err == nil || err.Error() != "The password needs at least 8 characters." {
		t.Errorf("createOwner = %v, want the rule in words", err)
	}
}

func TestCreateOwnerStopsAtEndOfInput(t *testing.T) {
	p, _ := pipe("Faiz\npemilik\n")
	if err := createOwner(t.Context(), &fakeOwners{}, p); err == nil {
		t.Error("createOwner succeeded without an email or password")
	}
}
