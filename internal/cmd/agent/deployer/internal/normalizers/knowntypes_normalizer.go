// +vendored argoproj/argo-cd/util/argo/normalizers/knowntypes_normalizer.go
package normalizers

import (
	"encoding/json"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/internal/resource"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var knownTypes = map[string]func() interface{}{}

const Group string = "argoproj.io"

type knownTypeField struct {
	fieldPath  []string
	newFieldFn func() interface{}
}

type knownTypesNormalizer struct {
	typeFields map[schema.GroupKind][]knownTypeField
}

// NewKnownTypesNormalizer create a normalizer that re-format custom resource fields using built-in Kubernetes types.
func NewKnownTypesNormalizer(overrides map[string]resource.ResourceOverride) (*knownTypesNormalizer, error) {
	normalizer := knownTypesNormalizer{typeFields: map[schema.GroupKind][]knownTypeField{}}
	for key, override := range overrides {
		group, kind, err := getGroupKindForOverrideKey(key)
		if err != nil {
			log.Warn(err)
		}
		gk := schema.GroupKind{Group: group, Kind: kind}
		for _, f := range override.KnownTypeFields {
			if err := normalizer.addKnownField(gk, f.Field, f.Type); err != nil {
				log.Warnf("Failed to configure known field normalizer: %v", err)
			}
		}
	}
	normalizer.ensureDefaultCRDsConfigured()
	return &normalizer, nil
}

func (n *knownTypesNormalizer) ensureDefaultCRDsConfigured() {
	rolloutGK := schema.GroupKind{Group: Group, Kind: "Rollout"}
	if _, ok := n.typeFields[rolloutGK]; !ok {
		n.typeFields[rolloutGK] = []knownTypeField{{
			fieldPath: []string{"spec", "template", "spec"},
			newFieldFn: func() interface{} {
				return &v1.PodSpec{}
			},
		}}
	}
}

func getGroupKindForOverrideKey(key string) (string, string, error) {
	var group, kind string
	parts := strings.Split(key, "/")

	switch len(parts) {
	case 2:
		group = parts[0]
		kind = parts[1]
	case 1:
		kind = parts[0]
	default:
		return "", "", fmt.Errorf("override key must be <group>/<kind> or <kind>, got: '%s' ", key)
	}
	return group, kind, nil
}

func (n *knownTypesNormalizer) addKnownField(gk schema.GroupKind, fieldPath string, typePath string) error {
	newFieldFn, ok := knownTypes[typePath]
	if !ok {
		return fmt.Errorf("type '%s' is not supported", typePath)
	}
	n.typeFields[gk] = append(n.typeFields[gk], knownTypeField{
		fieldPath:  strings.Split(fieldPath, "."),
		newFieldFn: newFieldFn,
	})
	return nil
}

func normalize(obj map[string]interface{}, field knownTypeField, fieldPath []string) error {
	for i := range fieldPath {
		if nestedField, ok, err := unstructured.NestedFieldNoCopy(obj, fieldPath[:i+1]...); err == nil && ok {
			items, ok := nestedField.([]interface{})
			if !ok {
				continue
			}
			for j := range items {
				item, ok := items[j].(map[string]interface{})
				if !ok {
					continue
				}

				subPath := fieldPath[i+1:]
				if len(subPath) == 0 {
					newItem, err := nremarshal(item, field)
					if err != nil {
						return err
					}
					items[j] = newItem
				} else {
					if err = normalize(item, field, subPath); err != nil {
						return err
					}
				}
			}
			return unstructured.SetNestedSlice(obj, items, fieldPath[:i+1]...)
		}
	}

	if fieldVal, ok, err := unstructured.NestedMap(obj, fieldPath...); ok && err == nil {
		newFieldVal, err := nremarshal(fieldVal, field)
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj, newFieldVal, fieldPath...)
		if err != nil {
			return err
		}
	}

	return nil
}

func nremarshal(fieldVal map[string]interface{}, field knownTypeField) (map[string]interface{}, error) {
	data, err := json.Marshal(fieldVal)
	if err != nil {
		return nil, err
	}
	typedValue := field.newFieldFn()
	err = json.Unmarshal(data, typedValue)
	if err != nil {
		return nil, err
	}
	data, err = json.Marshal(typedValue)
	if err != nil {
		return nil, err
	}
	newFieldVal := map[string]interface{}{}
	err = json.Unmarshal(data, &newFieldVal)
	if err != nil {
		return nil, err
	}
	return newFieldVal, nil
}

// Normalize re-format custom resource fields using built-in Kubernetes types JSON marshaler.
// This technique allows avoiding false drift detections in CRDs that import data structures from Kubernetes codebase.
func (n *knownTypesNormalizer) Normalize(un *unstructured.Unstructured) error {
	if fields, ok := n.typeFields[un.GroupVersionKind().GroupKind()]; ok {
		for _, field := range fields {
			err := normalize(un.Object, field, field.fieldPath)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
