package main

import (
	"bufio"
	"bytes"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/platform/config"
)

// TestRoutesMatchTheContract fails when a route the router serves is missing
// from api/openapi.yaml, or the contract describes one the router doesn't
// serve: the clients generate their code from the contract, so the two must
// never drift apart.
func TestRoutesMatchTheContract(t *testing.T) {
	var served []string
	param := regexp.MustCompile(`:([a-z_]+)`)
	for _, r := range newTestRouter(t, config.Config{}).Routes() {
		served = append(served, r.Method+" "+param.ReplaceAllString(r.Path, "{$1}"))
	}
	slices.Sort(served)

	contract := contractRoutes(t)
	for _, route := range served {
		if !slices.Contains(contract, route) {
			t.Errorf("%s is served but missing from api/openapi.yaml", route)
		}
	}
	for _, route := range contract {
		if !slices.Contains(served, route) {
			t.Errorf("%s is in api/openapi.yaml but the router doesn't serve it", route)
		}
	}
}

// contractFile is the contract, from this package's directory.
const contractFile = "../../api/openapi.yaml"

// contractRoutes reads "METHOD /path" for every operation in the contract.
// The file's layout is regular enough for a line scan: paths are indented
// two spaces under `paths:`, their operations four.
func contractRoutes(t *testing.T) []string {
	t.Helper()
	contract, err := os.ReadFile(contractFile)
	if err != nil {
		t.Fatal(err)
	}

	pathLine := regexp.MustCompile(`^  (/\S*):$`)
	opLine := regexp.MustCompile(`^    (get|put|post|patch|delete):$`)
	var routes []string
	inPaths, current := false, ""
	scanner := bufio.NewScanner(bytes.NewReader(contract))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false
		case !inPaths:
		case pathLine.MatchString(line):
			current = pathLine.FindStringSubmatch(line)[1]
		case opLine.MatchString(line):
			routes = append(routes, strings.ToUpper(opLine.FindStringSubmatch(line)[1])+" "+current)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(routes) == 0 {
		t.Fatal("found no operations in the contract")
	}
	slices.Sort(routes)
	return routes
}
