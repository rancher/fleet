package cleanup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRunConfiguresControllerRuntimeLogger guards against a regression where
// this entrypoint never calls ctrl.SetLogger: log.Log calls in the cleanup
// controller are then silently dropped instead of printed.
func TestRunConfiguresControllerRuntimeLogger(t *testing.T) {
	if !funcCallsSetLogger(t, "root.go", "Run") {
		t.Fatal("CleanUp.Run must call ctrl.SetLogger, or log.Log output " +
			"from the cleanup controller will be silently dropped")
	}
}

// funcCallsSetLogger reports whether the named function/method declared in
// file contains a call to some package's SetLogger.
func funcCallsSetLogger(t *testing.T, file, funcName string) bool {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	found := false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetLogger" {
				found = true
			}
			return true
		})
	}
	return found
}
