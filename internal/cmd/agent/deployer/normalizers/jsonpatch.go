package normalizers

import (
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type JSONPatch []byte

type JSONPatchNormalizer struct {
	patch map[schema.GroupVersionKind]map[objectset.ObjectKey][]JSONPatch
}

func (j *JSONPatchNormalizer) Add(gvk schema.GroupVersionKind, key objectset.ObjectKey, patch JSONPatch) {
	if j.patch == nil {
		j.patch = map[schema.GroupVersionKind]map[objectset.ObjectKey][]JSONPatch{}
	}
	if _, ok := j.patch[gvk]; !ok {
		j.patch[gvk] = map[objectset.ObjectKey][]JSONPatch{}
	}
	if _, ok := j.patch[gvk][key]; !ok {
		j.patch[gvk][key] = []JSONPatch{}
	}
	j.patch[gvk][key] = append(j.patch[gvk][key], patch)
}

func (j JSONPatchNormalizer) Normalize(un *unstructured.Unstructured) error {
	if un == nil {
		return nil
	}
	gvk := un.GroupVersionKind()
	metaObj, err := meta.Accessor(un)
	if err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	key := objectset.ObjectKey{
		Namespace: metaObj.GetNamespace(),
		Name:      metaObj.GetName(),
	}

	if !j.hasPatches(gvk, key) {
		// If there are no patches, skip marshalling and unmarshalling
		return nil
	}

	jsondata, err := un.MarshalJSON()
	if err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	patched := applyPatches(jsondata, j.patch[gvk][key])
	if err := un.UnmarshalJSON(patched); err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	return nil
}

func (j *JSONPatchNormalizer) hasPatches(gvk schema.GroupVersionKind, key objectset.ObjectKey) bool {
	gvkPatches, ok := j.patch[gvk]
	if !ok {
		return false
	}
	keyPatches, ok := gvkPatches[key]
	if !ok {
		return false
	}
	if len(keyPatches) == 0 {
		return false
	}
	return true
}

func applyPatches(jsondata []byte, patches []JSONPatch) []byte {
	for _, patch := range patches {
		p, err := jsonpatch.DecodePatch(patch)
		if err != nil {
			logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
			return nil
		}
		jsondata, err = p.Apply(jsondata)
		if err != nil {
			logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
			return nil
		}
	}
	return jsondata
}
