/*
Copyright 2020, 2021 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package update

import (
	"encoding/json"

	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/kustomize/kyaml/fieldmeta"
	"sigs.k8s.io/kustomize/kyaml/openapi"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// The implementation of this filter is adapted from
// [kyaml](https://github.com/kubernetes-sigs/kustomize/blob/kyaml/v0.10.16/kyaml/setters2/set.go),
// with the following changes:
//
// - it calls its callback for each field it sets
//
// - it will set all fields referring to a setter present in the
// schema -- this is behind a flag in the kyaml implementation, but
// the only desired mode of operation here
//
// - substitutions are not supported -- they are not used for image
// updates
//
// - no validation is done on the value being set -- since the schema
// is constructed here, it's assumed the values will be appropriate
//
// - only scalar nodes are considered (i.e., no sequence replacements)
//
// - only per-field schema references (those in a comment in the YAML)
// are considered -- these are the only ones relevant to image updates

type SetAllCallback struct {
	SettersSchema *spec.Schema
	Callback      func(setter, oldValue, newValue string)
}

func (s *SetAllCallback) Filter(object *yaml.RNode) (*yaml.RNode, error) {
	return object, accept(s, object, "", s.SettersSchema)
}

// visitor is provided to accept to walk the AST.
type visitor interface {
	// visitScalar is called for each scalar field value on a resource
	// node is the scalar field value
	// path is the path to the field; path elements are separated by '.'
	visitScalar(node *yaml.RNode, path string, schema *openapi.ResourceSchema) error
}

// getSchema returns per-field OpenAPI schema for a particular node.
func getSchema(r *yaml.RNode, settersSchema *spec.Schema) *openapi.ResourceSchema {
	// get the override schema if it exists on the field
	fm := fieldmeta.FieldMeta{SettersSchema: settersSchema}
	if err := fm.Read(r); err == nil && !fm.IsEmpty() {
		// per-field schema, this is fine
		if fm.Schema.Ref.String() != "" {
			// resolve the reference
			s, err := openapi.Resolve(&fm.Schema.Ref, settersSchema)
			if err == nil && s != nil {
				fm.Schema = *s
			}
		}
		return &openapi.ResourceSchema{Schema: &fm.Schema}
	}
	return nil
}

// accept walks the AST and calls the visitor at each scalar node.
func accept(v visitor, object *yaml.RNode, p string, settersSchema *spec.Schema) error {
	switch object.YNode().Kind {
	case yaml.DocumentNode:
		// Traverse the child of the document
		return accept(v, yaml.NewRNode(object.YNode()), p, settersSchema)
	case yaml.MappingNode:
		return object.VisitFields(func(node *yaml.MapNode) error {
			// Traverse each field value
			return accept(v, node.Value, p+"."+node.Key.YNode().Value, settersSchema)
		})
	case yaml.SequenceNode:
		return object.VisitElements(func(node *yaml.RNode) error {
			// Traverse each list element
			return accept(v, node, p, settersSchema)
		})
	case yaml.ScalarNode:
		fieldSchema := getSchema(object, settersSchema)
		return v.visitScalar(object, p, fieldSchema)
	}
	return nil
}

type setter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type extension struct {
	Setter *setter `json:"setter,omitempty"`
}

// set applies the value from ext to field
func (s *SetAllCallback) set(field *yaml.RNode, ext *extension, sch *spec.Schema) error {
	// check full setter
	if ext.Setter == nil {
		return nil
	}

	// this has a full setter, set its value
	old := field.YNode().Value
	field.YNode().Value = ext.Setter.Value
	s.Callback(ext.Setter.Name, old, ext.Setter.Value)

	// format the node so it is quoted if it is a string. If there is
	// type information on the setter schema, we use it.
	if len(sch.Type) > 0 {
		yaml.FormatNonStringStyle(field.YNode(), *sch)
	}
	return nil
}

// visitScalar
func (s *SetAllCallback) visitScalar(object *yaml.RNode, p string, fieldSchema *openapi.ResourceSchema) error {
	if fieldSchema == nil {
		return nil
	}
	// get the openAPI for this field describing how to apply the setter
	ext, err := getExtFromSchema(fieldSchema.Schema)
	if err != nil {
		return err
	}
	if ext == nil {
		return nil
	}

	// perform a direct set of the field if it matches
	err = s.set(object, ext, fieldSchema.Schema)
	return err
}

func getExtFromSchema(schema *spec.Schema) (*extension, error) {
	cep := schema.Extensions[K8sCliExtensionKey]
	if cep == nil {
		return nil, nil
	}
	b, err := json.Marshal(cep)
	if err != nil {
		return nil, err
	}
	val := &extension{}
	if err := json.Unmarshal(b, val); err != nil {
		return nil, err
	}
	return val, nil
}
