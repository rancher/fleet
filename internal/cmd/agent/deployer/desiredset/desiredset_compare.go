package desiredset

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"strings"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"

	data2 "github.com/rancher/fleet/internal/cmd/agent/deployer/data"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/data/convert"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/jsonmergepatch"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	LabelApplied = "objectset.rio.cattle.io/applied"
)

var (
	knownListKeys = map[string]bool{
		"apiVersion":    true,
		"containerPort": true,
		"devicePath":    true,
		"ip":            true,
		"kind":          true,
		"mountPath":     true,
		"name":          true,
		"port":          true,
		"topologyKey":   true,
		"type":          true,
	}
)

func prepareObjectForCreate(gvk schema.GroupVersionKind, obj runtime.Object) (runtime.Object, error) {
	serialized, err := serializeApplied(obj)
	if err != nil {
		return nil, err
	}

	obj = obj.DeepCopyObject()
	m, err := meta.Accessor(obj)
	if err != nil {
		return nil, err
	}
	annotations := m.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	annotations[LabelApplied] = appliedToAnnotation(serialized)
	m.SetAnnotations(annotations)

	typed, err := meta.TypeAccessor(obj)
	if err != nil {
		return nil, err
	}

	apiVersion, kind := gvk.ToAPIVersionAndKind()
	typed.SetAPIVersion(apiVersion)
	typed.SetKind(kind)

	return obj, nil
}

func modifiedObj(gvk schema.GroupVersionKind, newObject runtime.Object) ([]byte, error) {
	newObject, err := prepareObjectForCreate(gvk, newObject)
	if err != nil {
		return nil, err
	}

	modified, err := json.Marshal(newObject)

	return modified, err
}

func emptyMaps(data map[string]interface{}, keys ...string) bool {
	for _, key := range append(keys, "__invalid_key__") {
		if len(data) == 0 {
			// map is empty so all children are empty too
			return true
		} else if len(data) > 1 {
			// map has more than one key so not empty
			return false
		}

		value, ok := data[key]
		if !ok {
			// map has one key but not what we are expecting so not considered empty
			return false
		}

		data = convert.ToMapInterface(value)
	}

	return true
}

func sanitizePatch(patch []byte, removeObjectSetAnnotation bool) ([]byte, error) {
	mod := false
	data := map[string]interface{}{}
	err := json.Unmarshal(patch, &data)
	if err != nil {
		return nil, err
	}

	if _, ok := data["kind"]; ok {
		mod = true
		delete(data, "kind")
	}

	if _, ok := data["apiVersion"]; ok {
		mod = true
		delete(data, "apiVersion")
	}

	if _, ok := data["status"]; ok {
		mod = true
		delete(data, "status")
	}

	if deleted := removeCreationTimestamp(data); deleted {
		mod = true
	}

	if removeObjectSetAnnotation {
		metadata := convert.ToMapInterface(data2.GetValueN(data, "metadata"))
		annotations := convert.ToMapInterface(data2.GetValueN(data, "metadata", "annotations"))
		for k := range annotations {
			if strings.HasPrefix(k, LabelPrefix) {
				mod = true
				delete(annotations, k)
			}
		}
		if mod && len(annotations) == 0 {
			delete(metadata, "annotations")
			if len(metadata) == 0 {
				delete(data, "metadata")
			}
		}
	}

	if emptyMaps(data, "metadata", "annotations") {
		return []byte("{}"), nil
	}

	if !mod {
		return patch, nil
	}

	return json.Marshal(data)
}

func (o *desiredSet) applyPatch(ctx context.Context, gvk schema.GroupVersionKind, debugID string, oldObject, newObject runtime.Object) (bool, error) {
	logger := log.FromContext(ctx).WithName("CompareObjects")

	oldMetadata, err := meta.Accessor(oldObject)
	if err != nil {
		return false, err
	}

	modified, err := modifiedObj(gvk, newObject)
	if err != nil {
		return false, err
	}

	current, err := json.Marshal(oldObject)
	if err != nil {
		return false, err
	}

	patch, err := doPatch(logger, gvk, modified, current)
	if err != nil {
		return false, errors.Wrap(err, "patch generation")
	}

	if string(patch) == "{}" {
		return false, nil
	}

	patch, err = sanitizePatch(patch, false)
	if err != nil {
		return false, err
	}

	if string(patch) == "{}" {
		return false, nil
	}

	logger.V(4).Info("DesiredSet - Looking at Patch", "gvk", gvk, "namespace", oldMetadata.GetNamespace(), "name", oldMetadata.GetName(), "debugID", debugID, "patch", string(patch), "modified", string(modified), "current", string(current))

	patch, err = sanitizePatch(patch, true)
	if err != nil {
		return false, err
	}

	if string(patch) != "{}" {
		logger.V(1).Info("DesiredSet - Updated Plan", "gvk", gvk, "namespace", oldMetadata.GetNamespace(), "name", oldMetadata.GetName(), "debugID", debugID, "patch", string(patch))
		o.plan.Update.Add(gvk, oldMetadata.GetNamespace(), oldMetadata.GetName(), string(patch))
	}

	return true, nil
}

func (o *desiredSet) compareObjects(ctx context.Context, gvk schema.GroupVersionKind, debugID string, oldObject, newObject runtime.Object) error {
	logger := log.FromContext(ctx)
	oldMetadata, err := meta.Accessor(oldObject)
	if err != nil {
		return err
	}

	o.plan.Objects = append(o.plan.Objects, oldObject)

	if ran, err := o.applyPatch(ctx, gvk, debugID, oldObject, newObject); err != nil {
		return err
	} else if !ran {
		logger.V(1).Info("DesiredSet - No change", "gvk", gvk, "namespace", oldMetadata.GetNamespace(), "name", oldMetadata.GetName(), "debugID", debugID)
	}

	return nil
}

func removeCreationTimestamp(data map[string]interface{}) bool {
	metadata, ok := data["metadata"]
	if !ok {
		return false
	}

	data = convert.ToMapInterface(metadata)
	if _, ok := data["creationTimestamp"]; ok {
		delete(data, "creationTimestamp")
		return true
	}

	return false
}

func pruneList(data []interface{}) []interface{} {
	result := make([]interface{}, 0, len(data))
	for _, v := range data {
		switch typed := v.(type) {
		case map[string]interface{}:
			result = append(result, pruneValues(typed, true))
		case []interface{}:
			result = append(result, pruneList(typed))
		default:
			result = append(result, v)
		}
	}
	return result
}

func pruneValues(data map[string]interface{}, isList bool) map[string]interface{} {
	result := map[string]interface{}{}
	for k, v := range data {
		switch typed := v.(type) {
		case map[string]interface{}:
			result[k] = pruneValues(typed, false)
		case []interface{}:
			result[k] = pruneList(typed)
		default:
			if isList && knownListKeys[k] {
				result[k] = v
			} else {
				switch x := v.(type) {
				case string:
					if len(x) > 64 {
						result[k] = x[:64]
					} else {
						result[k] = v
					}
				case []byte:
					result[k] = nil
				default:
					result[k] = v
				}
			}
		}
	}
	return result
}

func serializeApplied(obj runtime.Object) ([]byte, error) {
	data, err := convert.EncodeToMap(obj)
	if err != nil {
		return nil, err
	}
	data = pruneValues(data, false)
	return json.Marshal(data)
}

func appliedToAnnotation(b []byte) string {
	buf := &bytes.Buffer{}
	w := gzip.NewWriter(buf)
	if _, err := w.Write(b); err != nil {
		return string(b)
	}
	if err := w.Close(); err != nil {
		return string(b)
	}
	return base64.RawStdEncoding.EncodeToString(buf.Bytes())
}

// doPatch is adapted from "kubectl apply"
func doPatch(logger logr.Logger, gvk schema.GroupVersionKind, modified, current []byte) ([]byte, error) {
	var (
		patchType types.PatchType
		patch     []byte
	)

	patchType, lookupPatchMeta, err := getMergeStyle(gvk)
	if err != nil {
		return nil, err
	}

	if patchType == types.StrategicMergePatchType {
		patch, err = strategicpatch.CreateThreeWayMergePatch(nil, modified, current, lookupPatchMeta, true)
	} else {
		patch, err = jsonmergepatch.CreateThreeWayJSONMergePatch(nil, modified, current)
	}

	if err != nil {
		logger.V(1).Error(err, "Failed to calculate patch", "gvk", gvk, "error", err)
	}

	return patch, err
}
