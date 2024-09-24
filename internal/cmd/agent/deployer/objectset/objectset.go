package objectset

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/merr"

	"github.com/rancher/wrangler/v3/pkg/schemes"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ObjectKey struct {
	Name      string
	Namespace string
}

func NewObjectKey(obj v1.Object) ObjectKey {
	return ObjectKey{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}

func (o ObjectKey) String() string {
	if o.Namespace == "" {
		return o.Name
	}
	return fmt.Sprintf("%s/%s", o.Namespace, o.Name)
}

type ObjectKeyByGVK map[schema.GroupVersionKind][]ObjectKey

type ObjectByGVK map[schema.GroupVersionKind]map[ObjectKey]runtime.Object

func (o ObjectByGVK) Add(obj runtime.Object) (schema.GroupVersionKind, error) {
	metadata, err := meta.Accessor(obj)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}

	gvk, err := getGVK(obj)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}

	objs := o[gvk]
	if objs == nil {
		objs = ObjectByKey{}
		o[gvk] = objs
	}

	objs[ObjectKey{
		Namespace: metadata.GetNamespace(),
		Name:      metadata.GetName(),
	}] = obj

	return gvk, nil
}

type ObjectSet struct {
	errs        []error
	objects     ObjectByGVK
	objectsByGK ObjectByGK
	order       []runtime.Object
	gvkOrder    []schema.GroupVersionKind
	gvkSeen     map[schema.GroupVersionKind]bool
}

func NewObjectSet(objs ...runtime.Object) *ObjectSet {
	os := &ObjectSet{
		objects:     ObjectByGVK{},
		objectsByGK: ObjectByGK{},
		gvkSeen:     map[schema.GroupVersionKind]bool{},
	}
	os.Add(objs...)
	return os
}

func (o *ObjectSet) ObjectsByGVK() ObjectByGVK {
	if o == nil {
		return nil
	}
	return o.objects
}

func (o *ObjectSet) Contains(gk schema.GroupKind, key ObjectKey) bool {
	_, ok := o.objectsByGK[gk][key]
	return ok
}

func (o *ObjectSet) Add(objs ...runtime.Object) *ObjectSet {
	for _, obj := range objs {
		o.add(obj)
	}
	return o
}

func (o *ObjectSet) add(obj runtime.Object) {
	if obj == nil || reflect.ValueOf(obj).IsNil() {
		return
	}

	gvk, err := o.objects.Add(obj)
	if err != nil {
		o.err(fmt.Errorf("failed to add %T: %w", obj, err))
		return
	}

	_, err = o.objectsByGK.add(obj)
	if err != nil {
		o.err(fmt.Errorf("failed to add %T: %w", obj, err))
		return
	}

	o.order = append(o.order, obj)
	if !o.gvkSeen[gvk] {
		o.gvkSeen[gvk] = true
		o.gvkOrder = append(o.gvkOrder, gvk)
	}
}

func (o *ObjectSet) err(err error) {
	o.errs = append(o.errs, err)
}

func (o *ObjectSet) Err() error {
	return merr.NewErrors(o.errs...)
}

func (o *ObjectSet) Len() int {
	return len(o.objects)
}

func (o *ObjectSet) GVKOrder(known ...schema.GroupVersionKind) []schema.GroupVersionKind {
	var rest []schema.GroupVersionKind

	for _, gvk := range known {
		if o.gvkSeen[gvk] {
			continue
		}
		rest = append(rest, gvk)
	}

	sort.Slice(rest, func(i, j int) bool {
		return rest[i].String() < rest[j].String()
	})

	return append(o.gvkOrder, rest...)
}

type ObjectByKey map[ObjectKey]runtime.Object

func (o ObjectByKey) Namespaces() []string {
	namespaces := Set{}
	for objKey := range o {
		namespaces.Add(objKey.Namespace)
	}
	return namespaces.Values()
}

type ObjectByGK map[schema.GroupKind]map[ObjectKey]runtime.Object

func (o ObjectByGK) add(obj runtime.Object) (schema.GroupKind, error) {
	metadata, err := meta.Accessor(obj)
	if err != nil {
		return schema.GroupKind{}, err
	}

	gvk, err := getGVK(obj)
	if err != nil {
		return schema.GroupKind{}, err
	}

	gk := gvk.GroupKind()

	objs := o[gk]
	if objs == nil {
		objs = ObjectByKey{}
		o[gk] = objs
	}

	objs[ObjectKey{
		Namespace: metadata.GetNamespace(),
		Name:      metadata.GetName(),
	}] = obj

	return gk, nil
}

func getGVK(obj runtime.Object) (schema.GroupVersionKind, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind != "" {
		return gvk, nil
	}

	gvks, _, err := schemes.All.ObjectKinds(obj)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("failed to find gvk for %T, you may need to import the wrangler generated controller package: %w", obj, err)
	}

	if len(gvks) == 0 {
		return schema.GroupVersionKind{}, fmt.Errorf("failed to find gvk for %T", obj)
	}

	return gvks[0], nil
}
