package validation

import (
	"reflect"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestParsePreservePath(t *testing.T) {
	field := func(f string) PreservePathToken { return PreservePathToken{Field: f} }
	list := func(k string) PreservePathToken { return PreservePathToken{ListKey: k} }

	valid := map[string][]PreservePathToken{
		"/spec/volumeName": {field("spec"), field("volumeName")},
		"/spec/template/metadata/labels/batch.kubernetes.io~1job-name": {
			field("spec"), field("template"), field("metadata"), field("labels"), field("batch.kubernetes.io/job-name"),
		},
		"/data/a~0b":                            {field("data"), field("a~b")},
		"/data/~01":                             {field("data"), field("~1")},
		"/spec/containers/[name]/volumeMounts":  {field("spec"), field("containers"), list("name"), field("volumeMounts")},
		"/a/[k]/b/[j~1x]/c":                     {field("a"), list("k"), field("b"), list("j/x"), field("c")},
		"/metadata/labels":                      {field("metadata"), field("labels")},
		"/metadata/annotations/status":          {field("metadata"), field("annotations"), field("status")},
		"/spec/status":                          {field("spec"), field("status")},
		"/metadata/finalizers":                  {field("metadata"), field("finalizers")},
		"/spec/[":                               {field("spec"), field("[")},
		"/items/[name]/metadata/name":           {field("items"), list("name"), field("metadata"), field("name")},
		"/metadata/namespaceSelector/something": {field("metadata"), field("namespaceSelector"), field("something")},
	}
	for path, want := range valid {
		t.Run(path, func(t *testing.T) {
			got, err := ParsePreservePath(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParsePreservePath(%q) = %+v, want %+v", path, got, want)
			}
		})
	}

	invalid := map[string]string{
		"":                                       `invalid JSON pointer "": must not be empty`,
		"spec.volumeName":                        `invalid JSON pointer "spec.volumeName": must start with "/", e.g. "/spec/volumeName"`,
		"/":                                      `invalid path "/": token 0 is empty`,
		"/spec//volumeName":                      `invalid path "/spec//volumeName": token 1 is empty`,
		"/spec/":                                 `invalid path "/spec/": token 1 is empty`,
		"/spec/a~2b":                             `invalid path "/spec/a~2b": "~" must be followed by "0" or "1"`,
		"/spec/a~":                               `invalid path "/spec/a~": "~" must be followed by "0" or "1"`,
		"/spec/containers/[]/x":                  `invalid path "/spec/containers/[]/x": list token "[]" must name a key, e.g. "[name]"`,
		"/spec/containers/[name]":                `invalid path "/spec/containers/[name]": list token "[name]" must be followed by a field and cannot be the first token`,
		"/[name]/x":                              `invalid path "/[name]/x": list token "[name]" must be followed by a field and cannot be the first token`,
		"/spec/containers/[a~2]/x":               `invalid path "/spec/containers/[a~2]/x": "~" must be followed by "0" or "1"`,
		"/a/[k]/[j]/x":                           `invalid path "/a/[k]/[j]/x": list token "[k]" must be followed by a field, not by list token "[j]"`,
		"/a/[k]/[]/x":                            `invalid path "/a/[k]/[]/x": list token "[k]" must be followed by a field, not by list token "[]"`,
		"/a/b/[k]/[j]":                           `invalid path "/a/b/[k]/[j]": list token "[k]" must be followed by a field, not by list token "[j]"`,
		"/status":                                `invalid path "/status": /status cannot be preserved`,
		"/status/phase":                          `invalid path "/status/phase": /status cannot be preserved`,
		"/apiVersion":                            `invalid path "/apiVersion": /apiVersion cannot be preserved`,
		"/kind":                                  `invalid path "/kind": /kind cannot be preserved`,
		"/metadata/name":                         `invalid path "/metadata/name": /metadata/name cannot be preserved`,
		"/metadata/namespace":                    `invalid path "/metadata/namespace": /metadata/namespace cannot be preserved`,
		"/metadata/uid":                          `invalid path "/metadata/uid": /metadata/uid cannot be preserved`,
		"/metadata/resourceVersion":              `invalid path "/metadata/resourceVersion": /metadata/resourceVersion cannot be preserved`,
		"/metadata/generation":                   `invalid path "/metadata/generation": /metadata/generation cannot be preserved`,
		"/metadata/creationTimestamp":            `invalid path "/metadata/creationTimestamp": /metadata/creationTimestamp cannot be preserved`,
		"/metadata/deletionTimestamp":            `invalid path "/metadata/deletionTimestamp": /metadata/deletionTimestamp cannot be preserved`,
		"/metadata/managedFields":                `invalid path "/metadata/managedFields": /metadata/managedFields cannot be preserved`,
		"/metadata/managedFields/[manager]/time": `invalid path "/metadata/managedFields/[manager]/time": /metadata/managedFields cannot be preserved`,
	}
	for path, want := range invalid {
		t.Run("invalid "+path, func(t *testing.T) {
			_, err := ParsePreservePath(path)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if len(err.Error()) < len(want) || err.Error()[:len(want)] != want {
				t.Errorf("got error %q, want it to start with %q", err, want)
			}
		})
	}
}

func TestValidatePreserveOnReplace(t *testing.T) {
	valid := fleet.PreserveOnReplaceRule{APIVersion: "v1", Kind: "PersistentVolumeClaim", Paths: []fleet.PreservedPath{{Path: "/spec/volumeName"}}}
	with := func(f func(*fleet.PreserveOnReplaceRule)) fleet.PreserveOnReplaceRule {
		r := valid
		r.Paths = append([]fleet.PreservedPath(nil), valid.Paths...)
		f(&r)
		return r
	}

	tests := map[string]struct {
		rules []fleet.PreserveOnReplaceRule
		want  string
	}{
		"no rules": {},
		"valid": {
			rules: []fleet.PreserveOnReplaceRule{
				valid,
				with(func(r *fleet.PreserveOnReplaceRule) { r.Name = "data-0" }),
				with(func(r *fleet.PreserveOnReplaceRule) {
					r.Namespace = "ns"
					r.NameRegex = "^debug-.*$"
					r.Paths = append(r.Paths, fleet.PreservedPath{Path: "/spec/volumes", MergeKey: "name"})
				}),
			},
		},
		"missing apiVersion": {
			rules: []fleet.PreserveOnReplaceRule{valid, with(func(r *fleet.PreserveOnReplaceRule) { r.APIVersion = "" })},
			want:  `prefix.helm.preserveOnReplace[1].apiVersion: must not be empty`,
		},
		"missing kind": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Kind = "" })},
			want:  `prefix.helm.preserveOnReplace[0].kind: must not be empty`,
		},
		"name is not a regular expression": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Name = "foo[" })},
		},
		"invalid nameRegex": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.NameRegex = "foo[" })},
			want:  `prefix.helm.preserveOnReplace[0].nameRegex: invalid regular expression "foo[": error parsing regexp: missing closing ]: ` + "`[`",
		},
		"name and nameRegex": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Name = "data-0"; r.NameRegex = "^data-" })},
		},
		"name and invalid nameRegex": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Name = "data-0"; r.NameRegex = "foo[" })},
		},
		"no paths": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Paths = nil })},
			want:  `prefix.helm.preserveOnReplace[0].paths: must not be empty`,
		},
		"invalid path": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) {
				r.Paths = append(r.Paths, fleet.PreservedPath{Path: "/spec/a"}, fleet.PreservedPath{Path: "spec.storageClassName"})
			})},
			want: `prefix.helm.preserveOnReplace[0].paths[2].path: invalid JSON pointer "spec.storageClassName": must start with "/", e.g. "/spec/storageClassName"`,
		},
		"never preserved path": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Paths[0].Path = "/status/phase" })},
			want:  `prefix.helm.preserveOnReplace[0].paths[0].path: invalid path "/status/phase": /status cannot be preserved`,
		},
		"blank mergeKey": {
			rules: []fleet.PreserveOnReplaceRule{with(func(r *fleet.PreserveOnReplaceRule) { r.Paths[0].MergeKey = " " })},
			want:  `prefix.helm.preserveOnReplace[0].paths[0].mergeKey: invalid key " ": must not start or end with white space`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := &fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{PreserveOnReplace: tt.rules}}
			err := ValidatePreserveOnReplace("prefix.", opts)
			if tt.want == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Errorf("got error %v, want %q", err, tt.want)
			}
		})
	}

	if err := ValidatePreserveOnReplace("", &fleet.BundleDeploymentOptions{}); err != nil {
		t.Errorf("unexpected error without Helm options: %v", err)
	}
}
