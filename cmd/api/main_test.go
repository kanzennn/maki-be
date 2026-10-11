package main

import (
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommands(t *testing.T) {
	// Rejected before any configuration is read, so a typo in a deploy
	// script fails loudly instead of starting the server.
	for _, args := range [][]string{{"seed"}, {"migrate", "down"}, {"-h"}} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("run(%q) error = %v, want an unknown command error", args, err)
		}
	}
}
