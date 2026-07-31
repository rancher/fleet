// Command jsonschema generates a JSON Schema for the fleet.yaml file from the
// FleetYAML Go struct. It is wired into `go generate` (see generate.go) so the
// schema is regenerated alongside the CRDs and stays in sync with the structs.
//
// It must be run from the repository root (as `go generate` and
// `go run ./cmd/codegen/jsonschema` both are), because it reads Go source
// comments from ./pkg/apis and writes the schema under ./schemas.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/sirupsen/logrus"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// moduleBase is joined with each walked source directory to reconstruct a
	// package's import path (AddGoComments keys comments by gopath.Join(base,
	// dir), matched against the reflected type's PkgPath). The fleet.yaml
	// structs live in the github.com/rancher/fleet/pkg/apis module, whose import
	// path is this base plus its on-disk path, so the repo module root is the
	// correct base.
	moduleBase = "github.com/rancher/fleet"
	// apisSrcDir is the on-disk location of that package, relative to the repo
	// root, walked to extract doc comments.
	apisSrcDir = "./pkg/apis/fleet.cattle.io/v1alpha1"

	outputPath = "schemas/fleet.yaml.json"
)

func main() {
	r := &jsonschema.Reflector{
		// Inline FleetYAML's own fields at the root of the schema instead of
		// hiding them behind a $ref, so the document reads as "a fleet.yaml".
		ExpandedStruct: true,
		// Map types that reflect poorly to their real JSON shape.
		Mapper: typeMapper,
	}

	// Pull `// ...` doc comments off the structs and turn them into schema
	// descriptions. This is what gives IDEs inline documentation.
	if err := r.AddGoComments(moduleBase, apisSrcDir); err != nil {
		logrus.Fatalf("adding Go comments: %v", err)
	}
	stripMarkers(r.CommentMap)

	schema := r.Reflect(&v1alpha1.FleetYAML{})

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		logrus.Fatalf("marshaling schema: %v", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		logrus.Fatalf("creating output directory: %v", err)
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil { //nolint:gosec
		logrus.Fatalf("writing schema: %v", err)
	}

	logrus.Infof("wrote %s", outputPath)
}

// stripMarkers removes code-generation marker lines from the extracted doc
// comments, in place. Markers such as `+optional`, `+nullable` and
// `+kubebuilder:validation:...` are directives to controller-gen rather than
// docs.
// TODO: Parse markers using https://pkg.go.dev/sigs.k8s.io/controller-tools/pkg/markers to get typed parsing instead of regex and map them ontoinvopop's schema fields.
func stripMarkers(comments map[string]string) {
	for key, comment := range comments {
		lines := strings.Split(comment, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "+") {
				continue
			}
			kept = append(kept, line)
		}

		if cleaned := strings.TrimSpace(strings.Join(kept, "\n")); cleaned != "" {
			comments[key] = cleaned
		} else {
			delete(comments, key)
		}
	}
}

// typeMapper overrides the schema for types whose Go representation does not
// reflect into a useful JSON Schema, either because their wire format comes
// from a custom MarshalJSON rather than from struct tags, or because they
// serialize to something other than their struct shape. Returning nil lets the
// reflector handle the type normally. Pointers are dereferenced so both value
// and pointer fields are covered.
func typeMapper(t reflect.Type) *jsonschema.Schema {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case reflect.TypeFor[intstr.IntOrString]():
		// Accepts either a string ("50%") or an integer (5).
		return &jsonschema.Schema{
			OneOf: []*jsonschema.Schema{
				{Type: "string"},
				{Type: "integer"},
			},
		}
	case reflect.TypeFor[metav1.Duration]():
		// Serialized as a Go duration string, e.g. "15s".
		return &jsonschema.Schema{Type: "string"}
	case reflect.TypeFor[v1alpha1.GenericMap]():
		// Its only field is tagged `json:"-"`; the arbitrary key/value pairs
		// reach the wire through its MarshalJSON. Reflected as-is it would
		// yield an empty object with additionalProperties false, rejecting
		// every key.
		return &jsonschema.Schema{Type: "object"}
	}
	return nil
}
