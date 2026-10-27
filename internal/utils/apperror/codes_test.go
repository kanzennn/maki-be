package apperror

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/utils/i18n"
)

// sends are the functions that send an error code: by package, each one's
// name and the position of its code argument.
var sends = map[string]map[string]int{
	"apperror": {"NewError": 1},   // apperror.NewError(status, code)
	"response": {"WriteError": 2}, // response.WriteError(c, status, code)
}

// Every error code the API can send needs a message in the catalogs. This
// reads every non-test Go file in the module, so a new error fails here —
// with the key to add — before any client sees a message ID.
func TestEveryErrorCodeHasAMessage(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	found := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "tmp" || d.Name() == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, arg, ok := errorFunc(call.Fun, file.Name.Name)
			if !ok || len(call.Args) <= arg {
				return true
			}
			pos := fset.Position(call.Pos())
			lit, ok := call.Args[arg].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: pass %s a string literal code, so this test can check its message", pos, name)
				return true
			}
			code, _ := strconv.Unquote(lit.Value)
			found++
			if !i18n.Has("error." + code) {
				t.Errorf("%s: error code %q has no message: add \"error.%s\" to every catalog in internal/utils/i18n/locales", pos, code, code)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Guards against the walk silently finding nothing, e.g. from the wrong
	// directory: the shared errors alone send five codes.
	if found < 5 {
		t.Fatalf("found %d error codes in %s, want at least 5", found, root)
	}
}

// errorFunc reports whether fun sends an error code — apperror.NewError or
// response.WriteError, or the bare name inside its own package — which one,
// and the position of its code argument.
func errorFunc(fun ast.Expr, inPackage string) (string, int, bool) {
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		if !ok {
			return "", 0, false
		}
		arg, known := sends[pkg.Name][f.Sel.Name]
		return f.Sel.Name, arg, known
	case *ast.Ident:
		arg, known := sends[inPackage][f.Name]
		return f.Name, arg, known
	}
	return "", 0, false
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
