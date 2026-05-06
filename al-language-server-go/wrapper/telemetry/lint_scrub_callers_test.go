package telemetry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryScrubCallerDeclaresSourceMode walks the wrapper Go source and
// asserts that every Scrub() call site uses a literal SourceMode constant
// as its second argument.
func TestEveryScrubCallerDeclaresSourceMode(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	wrapperDir := filepath.Join(repoRoot, "al-language-server-go", "wrapper")
	fset := token.NewFileSet()

	var failures []string

	err = filepath.Walk(wrapperDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Scrub" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "telemetry" {
				return true
			}
			if len(call.Args) < 2 {
				failures = append(failures, fset.Position(call.Pos()).String()+" Scrub() needs 3 args")
				return true
			}
			modeArg, ok := call.Args[1].(*ast.SelectorExpr)
			if !ok {
				failures = append(failures, fset.Position(call.Pos()).String()+" Scrub() second arg not a SourceMode literal")
				return true
			}
			if id, ok := modeArg.X.(*ast.Ident); !ok || id.Name != "telemetry" {
				failures = append(failures, fset.Position(call.Pos()).String()+" Scrub() second arg not from telemetry pkg")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(failures) > 0 {
		t.Errorf("scrub-caller lint failures:\n  %s", strings.Join(failures, "\n  "))
	}
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Dir(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", os.ErrNotExist
}
