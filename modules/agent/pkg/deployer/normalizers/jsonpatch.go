package normalizers

import (
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/rancher/wrangler/pkg/objectset"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type JSONPatchNormalizer struct {
	patch map[schema.GroupVersionKind]map[objectset.ObjectKey][]byte
}

func (j *JSONPatchNormalizer) Add(gvk schema.GroupVersionKind, key objectset.ObjectKey, patch []byte) {
	if j.patch == nil {
		j.patch = map[schema.GroupVersionKind]map[objectset.ObjectKey][]byte{}
	}
	if _, ok := j.patch[gvk]; !ok {
		j.patch[gvk] = map[objectset.ObjectKey][]byte{}
	}
	j.patch[gvk][key] = patch
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
	patch := j.patch[gvk][key]
	if patch == nil {
		return nil
	}
	p, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	jsondata, err := un.MarshalJSON()
	if err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	patched, err := p.Apply(jsondata)
	if err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	if err := un.UnmarshalJSON(patched); err != nil {
		logrus.Errorf("Failed to normalize obj with json patch, error: %v", err)
		return nil
	}
	return nil
}
