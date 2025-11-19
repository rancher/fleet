package helmdeployer

import (
	"reflect"
	"strings"
	"text/template"
	"text/template/parse"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
)

// hasLookupFunction checks if any template in the given Helm chart
// calls the "lookup" function. It parses the templates to ensure it's a function
// call and not just the word "lookup" in text or comments.
func hasLookupFunction(ch *chartv2.Chart) bool {
	for _, tpl := range ch.Templates {
		// Parse the template into an AST.
		t, err := template.New(
			tpl.Name,
		).Option(
			"missingkey=zero",
		).Funcs(
			map[string]interface{}{"lookup": func() error { return nil }},
		).Parse(string(tpl.Data))
		if err != nil {
			// Some templates might not parse correctly if they depend on values
			// that aren't available. We can safely ignore these errors and continue,
			// as a parse error means we couldn't definitively find a valid 'lookup' call.
			continue
		}

		// Walk all parse trees in this template and look for lookup invocations.
		if t.Tree != nil && t.Root != nil {
			if containsLookup(t.Root) {
				return true
			}
		}
	}

	return false
}

// containsLookup recursively checks whether a parse.Node (and its children)
// contains a call to the "lookup" function.
func containsLookup(node parse.Node) bool { //nolint:gocyclo // recursive logic
	if nodeIsNil(node) {
		return false
	}

	// Quick textual pre-check. If the node's string representation does not
	// contain the word "lookup", there's no need to traverse it deeply.
	// This avoids unnecessary recursion for nodes that clearly don't reference
	// the lookup function. If the textual representation contains
	// "lookup", fall through to the detailed inspection below.
	if !nodeExprContainsLookup(node) {
		return false
	}

	switch node.Type() {
	case parse.NodeAction:
		if n, ok := node.(*parse.ActionNode); ok && n != nil {
			return containsLookup(n.Pipe)
		}
		return false
	case parse.NodeIf:
		if n, ok := node.(*parse.IfNode); ok && n != nil {
			// check if any of the sub-nodes contain lookup
			return containsLookup(n.ElseList) || containsLookup(n.Pipe) || containsLookup(n.List)
		}
		return false
	case parse.NodeList:
		if n, ok := node.(*parse.ListNode); ok && n != nil {
			for _, subNode := range n.Nodes {
				if containsLookup(subNode) {
					return true
				}
			}
		}
		return false
	case parse.NodeRange:
		if n, ok := node.(*parse.RangeNode); ok && n != nil {
			// check if any of the sub-nodes contain lookup
			return containsLookup(n.ElseList) || containsLookup(n.Pipe) || containsLookup(n.List)
		}
		return false
	case parse.NodeTemplate:
		if n, ok := node.(*parse.TemplateNode); ok && n != nil {
			return containsLookup(n.Pipe)
		}
		return false
	case parse.NodeWith:
		if n, ok := node.(*parse.WithNode); ok && n != nil {
			// check if any of the sub-nodes contain lookup
			return containsLookup(n.Pipe) || containsLookup(n.List) || containsLookup(n.ElseList)
		}
		return false
	case parse.NodePipe:
		if n, ok := node.(*parse.PipeNode); ok && n != nil {
			for _, cmd := range n.Cmds {
				if containsLookup(cmd) {
					return true
				}
			}
		}
		return false
	case parse.NodeCommand:
		if n, ok := node.(*parse.CommandNode); ok && n != nil {
			for i, arg := range n.Args {
				// The first argument of a command node is usually the function name.
				if i == 0 {
					ident, ok := arg.(*parse.IdentifierNode)
					if ok && ident != nil && ident.Ident == "lookup" {
						return true
					}
				}
				// Recurse into arguments to find nested lookups, e.g., {{ template "foo" (lookup ...) }}
				if containsLookup(arg) {
					return true
				}
			}
		}
		return false
	case parse.NodeChain:
		if n, ok := node.(*parse.ChainNode); ok && n != nil {
			// Covers cases like (lookup ...).items where the lookup is part of a chained expression.
			if n.Node != nil {
				return containsLookup(n.Node)
			}
		}
		return false
	default:
		return false
	}
}

// nodeExprContainsLookup returns true when the node has a textual
// expression (via String()) and that textual representation contains the
// substring "lookup". This is a cheap pre-check to avoid deep traversal for
// nodes that don't reference the lookup function at all.
func nodeExprContainsLookup(node parse.Node) bool {
	if nodeIsNil(node) {
		return false
	}
	s := node.String()

	return strings.Contains(s, "lookup")
}

func nodeIsNil(node parse.Node) bool {
	if node == nil {
		return true
	}
	rv := reflect.ValueOf(node)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return true
	}
	return false
}
