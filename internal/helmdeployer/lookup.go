package helmdeployer

import (
	"text/template"
	"text/template/parse"

	"helm.sh/helm/v3/pkg/chart"
)

// hasLookupFunction checks if any template in the given Helm chart
// calls the "lookup" function. It parses the templates to ensure it's a function
// call and not just the word "lookup" in text or comments.
func hasLookupFunction(ch *chart.Chart) bool {
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
func containsLookup(node parse.Node) bool {
	if node == nil {
		return false
	}

	switch n := node.(type) {
	case *parse.ActionNode:
		return containsLookup(n.Pipe)
	case *parse.IfNode:
		return containsLookup(n.Pipe) || containsLookup(n.List) || containsLookup(n.ElseList)
	case *parse.ListNode:
		for _, subNode := range n.Nodes {
			if containsLookup(subNode) {
				return true
			}
		}
	case *parse.RangeNode:
		return containsLookup(n.Pipe) || containsLookup(n.List) || containsLookup(n.ElseList)
	case *parse.TemplateNode:
		return containsLookup(n.Pipe)
	case *parse.WithNode:
		return containsLookup(n.Pipe) || containsLookup(n.List) || containsLookup(n.ElseList)
	case *parse.PipeNode:
		for _, cmd := range n.Cmds {
			if containsLookup(cmd) {
				return true
			}
		}
	case *parse.CommandNode:
		// The first argument of a command node is usually the function name.
		if len(n.Args) > 0 {
			if ident, ok := n.Args[0].(*parse.IdentifierNode); ok && ident.Ident == "lookup" {
				return true
			}
		}
		// Recurse into arguments to find nested lookups, e.g., {{ template "foo" (lookup ...) }}
		for _, arg := range n.Args {
			if containsLookup(arg) {
				return true
			}
		}
	}

	return false
}
