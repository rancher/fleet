package helmdeployer

import (
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-cmp/cmp"

	"github.com/rancher/fleet/internal/validation"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// obj decodes JSON the way replaceTransport does.
func obj(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := decodeJSON([]byte(s), &m); err != nil {
		t.Fatalf("invalid test JSON %s: %v", s, err)
	}
	return m
}

func mustCompile(t *testing.T, entries ...fleet.PreserveOnReplaceRule) []preserveRule {
	t.Helper()
	rules, err := compilePreserveRules(entries)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestPreserveLiveFields(t *testing.T) {
	tests := map[string]struct {
		target   string
		live     string
		paths    []fleet.PreservedPath
		expected string
		changed  bool
		wantErr  string
	}{
		"copies a value the manifest leaves unset": {
			target:   `{"spec":{"a":"git"}}`,
			live:     `{"spec":{"a":"live","b":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"a":"git","b":"live"}}`,
			changed:  true,
		},
		"copies over null": {
			target:   `{"spec":{"b":null}}`,
			live:     `{"spec":{"b":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"b":"live"}}`,
			changed:  true,
		},
		"git value wins": {
			target:   `{"spec":{"b":"git"}}`,
			live:     `{"spec":{"b":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"b":"git"}}`,
		},
		"explicit empty string wins": {
			target:   `{"spec":{"b":""}}`,
			live:     `{"spec":{"b":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"b":""}}`,
		},
		"explicit empty object wins": {
			target:   `{"spec":{"b":{}}}`,
			live:     `{"spec":{"b":{"c":1}}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"b":{}}}`,
		},
		"explicit empty list wins": {
			target:   `{"spec":{"b":[]}}`,
			live:     `{"spec":{"b":[1]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"b":[]}}`,
		},
		"unset on the live object": {
			target:   `{"spec":{}}`,
			live:     `{"spec":{}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{}}`,
		},
		"null on the live object": {
			target:   `{"spec":{}}`,
			live:     `{"spec":{"b":null}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{}}`,
		},
		"creates missing intermediate objects": {
			target:   `{"metadata":{"name":"x"}}`,
			live:     `{"spec":{"template":{"metadata":{"labels":{"job-name":"x"}}}}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/template/metadata/labels/job-name"}},
			expected: `{"metadata":{"name":"x"},"spec":{"template":{"metadata":{"labels":{"job-name":"x"}}}}}`,
			changed:  true,
		},
		"replaces a null intermediate object": {
			target:   `{"spec":null}`,
			live:     `{"spec":{"b":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":{"b":"live"}}`,
			changed:  true,
		},
		"adds no empty intermediate object": {
			target:   `{}`,
			live:     `{"spec":{"other":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{}`,
		},
		"escaped tokens": {
			target:   `{"metadata":{"labels":{}}}`,
			live:     `{"metadata":{"labels":{"batch.kubernetes.io/job-name":"x","a~b":"y"}}}`,
			paths:    []fleet.PreservedPath{{Path: "/metadata/labels/batch.kubernetes.io~1job-name"}, {Path: "/metadata/labels/a~0b"}},
			expected: `{"metadata":{"labels":{"batch.kubernetes.io/job-name":"x","a~b":"y"}}}`,
			changed:  true,
		},
		"keeps numbers as written": {
			target:   `{}`,
			live:     `{"spec":{"n":9007199254740993}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/n"}},
			expected: `{"spec":{"n":9007199254740993}}`,
			changed:  true,
		},
		"a string in the manifest where the path needs an object": {
			target:   `{"spec":"git"}`,
			live:     `{"spec":{"b":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{"spec":"git"}`,
			wantErr:  `path "/spec/b": "spec" is not an object in the manifest`,
		},
		"a string on the live object where the path needs an object": {
			target:   `{}`,
			live:     `{"spec":"live"}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}},
			expected: `{}`,
			wantErr:  `path "/spec/b": "spec" is not an object on the live object`,
		},
		"a list index instead of a list token": {
			target:   `{"spec":{"containers":[{"name":"a"}]}}`,
			live:     `{"spec":{"containers":[{"name":"a","x":1}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/containers/0/x"}},
			expected: `{"spec":{"containers":[{"name":"a"}]}}`,
			wantErr:  `path "/spec/containers/0/x": "containers" is not an object on the live object`,
		},
		"an error does not stop other paths": {
			target:   `{"spec":"git","data":{}}`,
			live:     `{"spec":{"b":"live"},"data":{"c":"live"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/b"}, {Path: "/data/c"}},
			expected: `{"spec":"git","data":{"c":"live"}}`,
			changed:  true,
			wantErr:  `"spec" is not an object in the manifest`,
		},

		// [key] tokens
		"pairs list items by key": {
			target: `{"spec":{"containers":[{"name":"b"},{"name":"a","volumeMounts":null},{"name":"new"}]}}`,
			live: `{"spec":{"containers":[
				{"name":"a","volumeMounts":[{"mountPath":"/a"}]},
				{"name":"b","volumeMounts":[{"mountPath":"/b"}]},
				{"name":"gone","volumeMounts":[{"mountPath":"/gone"}]}]}}`,
			paths: []fleet.PreservedPath{{Path: "/spec/containers/[name]/volumeMounts"}},
			expected: `{"spec":{"containers":[
				{"name":"b","volumeMounts":[{"mountPath":"/b"}]},
				{"name":"a","volumeMounts":[{"mountPath":"/a"}]},
				{"name":"new"}]}}`,
			changed: true,
		},
		"skips items without the key": {
			target:   `{"spec":{"containers":[{"image":"x"}]}}`,
			live:     `{"spec":{"containers":[{"image":"x","volumeMounts":[]}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/containers/[name]/volumeMounts"}},
			expected: `{"spec":{"containers":[{"image":"x"}]}}`,
		},
		"skips live items without the key": {
			target:   `{"spec":{"containers":[{"name":"a"}]}}`,
			live:     `{"spec":{"containers":[{"volumeMounts":[{"mountPath":"/a"}]}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/containers/[name]/volumeMounts"}},
			expected: `{"spec":{"containers":[{"name":"a"}]}}`,
		},
		"no list in the manifest": {
			target:   `{"spec":{}}`,
			live:     `{"spec":{"containers":[{"name":"a","x":1}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/containers/[name]/x"}},
			expected: `{"spec":{}}`,
		},
		"list token on a non-list": {
			target:   `{"spec":{"containers":{"name":"a"}}}`,
			live:     `{"spec":{"containers":[{"name":"a","x":1}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/containers/[name]/x"}},
			expected: `{"spec":{"containers":{"name":"a"}}}`,
			wantErr:  `"containers" is not a list in the manifest`,
		},
		"nested list tokens": {
			target:   `{"a":[{"k":"1","b":[{"j":"x"}]}]}`,
			live:     `{"a":[{"k":"1","b":[{"j":"x","v":"live"}]}]}`,
			paths:    []fleet.PreservedPath{{Path: "/a/[k]/b/[j]/v"}},
			expected: `{"a":[{"k":"1","b":[{"j":"x","v":"live"}]}]}`,
			changed:  true,
		},
		"pairs by numeric key": {
			target:   `{"ports":[{"port":80}]}`,
			live:     `{"ports":[{"port":443,"nodePort":1},{"port":80,"nodePort":2}]}`,
			paths:    []fleet.PreservedPath{{Path: "/ports/[port]/nodePort"}},
			expected: `{"ports":[{"port":80,"nodePort":2}]}`,
			changed:  true,
		},

		// mergeKey
		"merge appends live items missing from git, in live order": {
			target:   `{"spec":{"volumes":[{"name":"b","git":true}]}}`,
			live:     `{"spec":{"volumes":[{"name":"z"},{"name":"b","git":false},{"name":"a"}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{"volumes":[{"name":"b","git":true},{"name":"z"},{"name":"a"}]}}`,
			changed:  true,
		},
		"merge with nothing missing": {
			target:   `{"spec":{"volumes":[{"name":"a"}]}}`,
			live:     `{"spec":{"volumes":[{"name":"a","x":1}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{"volumes":[{"name":"a"}]}}`,
		},
		"merge skips live items without the key": {
			target:   `{"spec":{"volumes":[]}}`,
			live:     `{"spec":{"volumes":[{"x":1},{"name":"a"}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{"volumes":[{"name":"a"}]}}`,
			changed:  true,
		},
		"merge copies the list when the manifest has none": {
			target:   `{"spec":{}}`,
			live:     `{"spec":{"volumes":[{"name":"a"}]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{"volumes":[{"name":"a"}]}}`,
			changed:  true,
		},
		"merge with an empty live list": {
			target:   `{"spec":{}}`,
			live:     `{"spec":{"volumes":[]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{}}`,
		},
		"merge skips live items which are not objects": {
			target:   `{"spec":{"volumes":[{"name":"a"}]}}`,
			live:     `{"spec":{"volumes":["x",{"name":"b"},1]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{"volumes":[{"name":"a"},{"name":"b"}]}}`,
			changed:  true,
		},
		"merge on a list of scalars": {
			target:   `{"spec":{"args":["a"]}}`,
			live:     `{"spec":{"args":["a","b"]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/args", MergeKey: "name"}},
			expected: `{"spec":{"args":["a"]}}`,
		},
		"merge copies a list of scalars when the manifest has none": {
			target:   `{"spec":{}}`,
			live:     `{"spec":{"args":["a","b"]}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/args", MergeKey: "name"}},
			expected: `{"spec":{"args":["a","b"]}}`,
			changed:  true,
		},
		"merge on a non-list": {
			target:   `{"spec":{"volumes":[]}}`,
			live:     `{"spec":{"volumes":"x"}}`,
			paths:    []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}},
			expected: `{"spec":{"volumes":[]}}`,
			wantErr:  `"volumes" is not a list on the live object`,
		},
		"merge below a list token": {
			target: `{"spec":{"containers":[{"name":"a","volumeMounts":[{"mountPath":"/git"}]}]}}`,
			live: `{"spec":{"containers":[{"name":"a","volumeMounts":[
				{"mountPath":"/var/run/secrets/kubernetes.io/serviceaccount","name":"token"},{"mountPath":"/git"}]}]}}`,
			paths: []fleet.PreservedPath{{Path: "/spec/containers/[name]/volumeMounts", MergeKey: "mountPath"}},
			expected: `{"spec":{"containers":[{"name":"a","volumeMounts":[
				{"mountPath":"/git"},{"mountPath":"/var/run/secrets/kubernetes.io/serviceaccount","name":"token"}]}]}}`,
			changed: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rules := mustCompile(t, fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Paths: tt.paths})
			target, live := obj(t, tt.target), obj(t, tt.live)

			changed, err := preserveLiveFields(logr.Discard(), target, live, rules[0].paths)

			if tt.wantErr == "" && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if changed != tt.changed {
				t.Errorf("changed = %v, want %v", changed, tt.changed)
			}
			if diff := cmp.Diff(obj(t, tt.expected), target); diff != "" {
				t.Errorf("unexpected target (-want +got):\n%s", diff)
			}
		})
	}
}

// Live items which mergeKey cannot merge are logged at V(1).
func TestPreserveLiveFieldsLogsSkippedItems(t *testing.T) {
	var lines []string
	logger := funcr.New(func(prefix, args string) { lines = append(lines, args) }, funcr.Options{Verbosity: 1})
	rules := mustCompile(t, fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Paths: []fleet.PreservedPath{{Path: "/spec/args", MergeKey: "name"}}})

	if _, err := preserveLiveFields(logger, obj(t, `{"spec":{"args":["a"]}}`), obj(t, `{"spec":{"args":["a","b"]}}`), rules[0].paths); err != nil {
		t.Fatal(err)
	}

	logged := strings.Join(lines, "\n")
	for _, want := range []string{`"level"=1`, `"path"="/spec/args"`, `"skipped"=2`} {
		if !strings.Contains(logged, want) {
			t.Errorf("expected a log containing %s, got %q", want, logged)
		}
	}
}

// A path starting with a list token, which validation rejects, is reported
// rather than silently skipped.
func TestPreserveAtRejectsLeadingListToken(t *testing.T) {
	tokens := []validation.PreservePathToken{{ListKey: "k"}, {Field: "x"}}

	changed, err := preserveAt(logr.Discard(), obj(t, `{}`), obj(t, `{"x":1}`), tokens, "")

	if changed || err == nil {
		t.Errorf("preserveAt() = %v, %v, want false and an error", changed, err)
	}
}

// The live object must not be modified, nor share values with the target.
func TestPreserveLiveFieldsCopiesValues(t *testing.T) {
	target := obj(t, `{"spec":{}}`)
	live := obj(t, `{"spec":{"selector":{"matchLabels":{"a":"b"}}}}`)
	rules := mustCompile(t, fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Paths: []fleet.PreservedPath{{Path: "/spec/selector"}}})

	if _, err := preserveLiveFields(logr.Discard(), target, live, rules[0].paths); err != nil {
		t.Fatal(err)
	}
	target["spec"].(map[string]any)["selector"].(map[string]any)["matchLabels"].(map[string]any)["a"] = "changed"

	if diff := cmp.Diff(obj(t, `{"spec":{"selector":{"matchLabels":{"a":"b"}}}}`), live); diff != "" {
		t.Errorf("live object modified (-want +got):\n%s", diff)
	}
}

func TestPreserveRuleMatches(t *testing.T) {
	type object struct{ apiVersion, kind, namespace, name string }
	tests := map[string]struct {
		rule fleet.PreserveOnReplaceRule
		obj  object
		want bool
	}{
		"apiVersion and kind":           {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, object{"v1", "PersistentVolumeClaim", "ns", "data"}, true},
		"other kind":                    {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "PersistentVolumeClaim"}, object{"v1", "Pod", "ns", "data"}, false},
		"other apiVersion":              {fleet.PreserveOnReplaceRule{APIVersion: "batch/v1", Kind: "Job"}, object{"batch/v2", "Job", "ns", "data"}, false},
		"apiVersion without group":      {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Job"}, object{"batch/v1", "Job", "ns", "data"}, false},
		"kind is case sensitive":        {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "pod"}, object{"v1", "Pod", "ns", "data"}, false},
		"kind is not a glob":            {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "*"}, object{"v1", "Pod", "ns", "data"}, false},
		"namespace":                     {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Namespace: "ns"}, object{"v1", "Pod", "ns", "x"}, true},
		"other namespace":               {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Namespace: "ns"}, object{"v1", "Pod", "other", "x"}, false},
		"namespace is not a regexp":     {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Namespace: "n."}, object{"v1", "Pod", "ns", "x"}, false},
		"no namespace matches any":      {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod"}, object{"v1", "Pod", "any", "x"}, true},
		"no namespace, cluster scoped":  {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Namespace"}, object{"v1", "Namespace", "", "x"}, true},
		"exact name":                    {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "data"}, object{"v1", "Pod", "ns", "data"}, true},
		"exact name with regexp chars":  {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "a.b"}, object{"v1", "Pod", "ns", "a.b"}, true},
		"name is not a regexp":          {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "a.b"}, object{"v1", "Pod", "ns", "axb"}, false},
		"name is not a prefix":          {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "data"}, object{"v1", "Pod", "ns", "data-2"}, false},
		"name not matching":             {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "data"}, object{"v1", "Pod", "ns", "other"}, false},
		"name regexp":                   {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", NameRegex: "debug-.*"}, object{"v1", "Pod", "ns", "debug-1"}, true},
		"name regexp is unanchored":     {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", NameRegex: "data"}, object{"v1", "Pod", "ns", "data-2"}, true},
		"anchored name regexp":          {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", NameRegex: "^data$"}, object{"v1", "Pod", "ns", "data-2"}, false},
		"name regexp not matching":      {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", NameRegex: "^data"}, object{"v1", "Pod", "ns", "other"}, false},
		"name wins over name regexp":    {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "data", NameRegex: ".*"}, object{"v1", "Pod", "ns", "other"}, false},
		"name regexp ignored with name": {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Name: "data", NameRegex: "foo["}, object{"v1", "Pod", "ns", "data"}, true},
		"no name matches any":           {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod"}, object{"v1", "Pod", "ns", "anything"}, true},
		"namespace and name both match": {fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Namespace: "ns", NameRegex: "^d"}, object{"v1", "Pod", "ns", "data"}, true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tt.rule.Paths = []fleet.PreservedPath{{Path: "/spec/x"}}
			r := mustCompile(t, tt.rule)[0]
			if got := r.matches(tt.obj.apiVersion, tt.obj.kind, tt.obj.namespace, tt.obj.name); got != tt.want {
				t.Errorf("matches(%v) = %v, want %v", tt.obj, got, tt.want)
			}
		})
	}
}

func TestCompilePreserveRulesSkipsInvalidEntries(t *testing.T) {
	rules, err := compilePreserveRules([]fleet.PreserveOnReplaceRule{
		{APIVersion: "v1", Kind: "Pod", NameRegex: "foo[", Paths: []fleet.PreservedPath{{Path: "/spec/a"}}},
		{APIVersion: "v1", Kind: "Pod", Paths: []fleet.PreservedPath{{Path: "/status/a"}}},
		{APIVersion: "v1", Kind: "PersistentVolumeClaim", Paths: []fleet.PreservedPath{{Path: "/spec/volumeName"}}},
	})

	if len(rules) != 1 || rules[0].kind != "PersistentVolumeClaim" {
		t.Errorf("expected only the valid rule, got %+v", rules)
	}
	if err == nil ||
		!strings.Contains(err.Error(), "helm.preserveOnReplace[0].nameRegex") ||
		!strings.Contains(err.Error(), "helm.preserveOnReplace[1].paths[0].path") {
		t.Errorf("expected errors for entries 0 and 1, got %v", err)
	}
}

func TestPreservedPathsCollectsAllMatchingRules(t *testing.T) {
	rules := mustCompile(t,
		fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Paths: []fleet.PreservedPath{{Path: "/spec/nodeName"}}},
		fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", NameRegex: "debug-.*", Paths: []fleet.PreservedPath{{Path: "/spec/volumes", MergeKey: "name"}}},
		fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "Pod", Namespace: "other", Paths: []fleet.PreservedPath{{Path: "/spec/x"}}},
	)

	var got []string
	for _, p := range preservedPaths(rules, "v1", "Pod", "ns", "debug-1") {
		got = append(got, p.path+"|"+p.mergeKey)
	}
	if diff := cmp.Diff([]string{"/spec/nodeName|", "/spec/volumes|name"}, got); diff != "" {
		t.Errorf("unexpected paths (-want +got):\n%s", diff)
	}
}
