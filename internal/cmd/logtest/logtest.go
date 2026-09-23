// Package logtest provides a regression check shared by fleetcli and
// fleet-controller subcommand packages: any Run method that uses the
// controller-runtime logger must also configure it via ctrl.SetLogger, or
// its log output is silently dropped.
package logtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// AssertPackageConfiguresLogger scans every non-test .go file in the
// caller's directory. For each Run method whose body uses the
// controller-runtime logger, it fails t if that method does not also call
// SetLogger.
func AssertPackageConfiguresLogger(t *testing.T) {
	t.Helper()

	_, callerFile, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("failed to locate caller")
	}
	dir := filepath.Dir(callerFile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			checkLoggerSetup(t, filepath.Join(dir, entry.Name()))
		})
	}
}

func checkLoggerSetup(t *testing.T, file string) {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Run" || fn.Body == nil || fn.Recv == nil ||
			!usesControllerRuntimeLogger(fn.Body) {
			continue
		}
		if !containsSetLogger(fn.Body) {
			t.Errorf("%s.Run must call ctrl.SetLogger", receiverName(fn.Recv.List[0].Type))
		}
	}
}

func usesControllerRuntimeLogger(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "IntoContext" || selectorChainContains(selector, "Log")) {
			found = true
		}
		return true
	})
	return found
}

func containsSetLogger(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "SetLogger" {
				found = true
			}
		}
		return !found
	})
	return found
}

func selectorChainContains(selector *ast.SelectorExpr, name string) bool {
	if selector.Sel.Name == name {
		return true
	}
	nested, ok := selector.X.(*ast.SelectorExpr)
	return ok && selectorChainContains(nested, name)
}

func receiverName(expr ast.Expr) string {
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = pointer.X
	}
	if identifier, ok := expr.(*ast.Ident); ok {
		return identifier.Name
	}
	return ""
}
