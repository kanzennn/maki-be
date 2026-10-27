// Package tests holds the tests that span the layers: the architecture's
// import rules here, and the integration tests that drive each resource's
// endpoints into a database (one folder per resource).
package tests

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/kervzent/maki-be"

// layers are what each layer of internal/ may import of the others. The
// dependencies point one way: routes → middlewares and controllers →
// services → repositories → the database, with models, validations, utils
// and config underneath.
var layers = map[string][]string{
	"config":       {},
	"utils":        {"config", "utils"},
	"models":       {"utils"},
	"validations":  {"models", "utils"},
	"repositories": {"models", "utils", "config", "repositories"},
	"services":     {"repositories", "models", "validations", "utils", "config", "services"},
	"controllers":  {"services", "validations", "models", "utils", "controllers"},
	"middlewares":  {"models", "utils", "middlewares"},
	"routes":       {"controllers", "middlewares", "models", "utils", "config", "routes"},
	"jobs":         {"services", "models", "utils", "jobs"},
	"tests":        nil, // the tests' own helpers may reach anything
}

// libraries are third-party packages a layer must not import, by path prefix.
var libraries = map[string][]string{
	"models":       {"github.com/gin-gonic/gin", "github.com/jackc/pgx"},
	"validations":  {"github.com/jackc/pgx"},
	"services":     {"github.com/gin-gonic/gin", "github.com/jackc/pgx"},
	"controllers":  {"github.com/jackc/pgx"},
	"middlewares":  {"github.com/jackc/pgx"},
	"jobs":         {"github.com/gin-gonic/gin", "github.com/jackc/pgx"},
	"repositories": {"github.com/gin-gonic/gin"},
}

func TestLayersImportOnlyWhatTheyMay(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "internal")
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		layer := parts[0]
		allowed, known := layers[layer]
		if !known {
			t.Errorf("%s: internal/%s is no layer of the architecture", rel, layer)
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			for _, lib := range libraries[layer] {
				if p == lib || strings.HasPrefix(p, lib+"/") {
					t.Errorf("%s: %s imports %s, which it must not", rel, layer, p)
				}
			}
			inner, ok := strings.CutPrefix(p, module+"/internal/")
			if !ok {
				continue
			}
			target := strings.Split(inner, "/")[0]
			// Only a repository opens the queries sqlc generated.
			if strings.HasPrefix(inner, "repositories/store/") && layer != "repositories" {
				t.Errorf("%s: %s imports %s; only repositories may use the generated queries", rel, layer, p)
				continue
			}
			if allowed != nil && !slices.Contains(allowed, target) {
				t.Errorf("%s: %s imports %s; %s may import only %s", rel, layer, p, layer, strings.Join(allowed, ", "))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("checked %d files under %s, want the layers' files", checked, root)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
