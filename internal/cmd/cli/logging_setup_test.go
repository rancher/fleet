package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestApplyRunConfiguresControllerRuntimeLogger guards against a regression
// where Apply.Run never calls ctrl.SetLogger: log.Log calls in bundlereader
// (e.g. the helmRepoURLRegex credential-stripping warning) are then silently
// dropped instead of printed in the apply job's logs.
func TestApplyRunConfiguresControllerRuntimeLogger(t *testing.T) {
	if !funcCallsSetLogger(t, "apply.go", "Run") {
		t.Fatal("Apply.Run must call ctrl.SetLogger, or log.Log output from " +
			"bundlereader will be silently dropped")
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
