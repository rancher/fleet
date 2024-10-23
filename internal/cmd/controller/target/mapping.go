package target

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BundleMapping is created from a BundleNamespaceMapping resource
type BundleMapping struct {
	namespace         string
	namespaceSelector labels.Selector
	bundleSelector    labels.Selector
	noMatch           bool
}

func newBundleMapping(mapping *fleet.BundleNamespaceMapping) (*BundleMapping, error) {
	var (
		result = &BundleMapping{
			namespace: mapping.Namespace,
		}
		err error
	)

	if mapping.BundleSelector == nil || mapping.NamespaceSelector == nil {
		result.noMatch = true
		return result, nil
	}

	result.bundleSelector, err = metav1.LabelSelectorAsSelector(mapping.BundleSelector)
	if err != nil {
		return nil, err
	}

	result.namespaceSelector, err = metav1.LabelSelectorAsSelector(mapping.NamespaceSelector)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (b *BundleMapping) Bundles(ctx context.Context, c client.Client) ([]*fleet.Bundle, error) {
	if b.noMatch {
		return nil, nil
	}
	list := &fleet.BundleList{}
	err := c.List(ctx, list, client.InNamespace(b.namespace), client.MatchingLabelsSelector{Selector: b.bundleSelector})

	bundles := make([]*fleet.Bundle, len(list.Items))
	for i := range list.Items {
		bundles[i] = &list.Items[i]
	}
	return bundles, err
}

func (b *BundleMapping) MatchesNamespace(ctx context.Context, c client.Client, namespace string) bool {
	if b.noMatch {
		return false
	}

	ns := &corev1.Namespace{}
	err := c.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		return false
	}
	return b.namespaceSelector.Matches(labels.Set(ns.Labels))
}

func (b *BundleMapping) Matches(bundle *fleet.Bundle) bool {
	if b.noMatch {
		return false
	}
	if bundle.Namespace != b.namespace {
		return false
	}
	return b.bundleSelector.Matches(labels.Set(bundle.Labels))
}

func (b *BundleMapping) Namespaces(ctx context.Context, c client.Client) ([]metav1.PartialObjectMetadata, error) {
	if b.noMatch {
		return nil, nil
	}

	list := &metav1.PartialObjectMetadataList{}
	list.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace"))
	err := c.List(ctx, list, client.MatchingLabelsSelector{Selector: b.namespaceSelector})
	return list.Items, err
}

type bundleSet struct {
	bundleKeys sets.Set[string]
	bundleMap  map[string]*fleet.Bundle
}

func newBundleSet() *bundleSet {
	return &bundleSet{
		bundleKeys: sets.New[string](),
		bundleMap:  map[string]*fleet.Bundle{},
	}
}

func (b *bundleSet) bundles() []*fleet.Bundle {
	var result []*fleet.Bundle
	// list is sorted
	for _, key := range sets.List(b.bundleKeys) {
		result = append(result, b.bundleMap[key])
	}
	return result
}

func (b *bundleSet) insert(bundles []*fleet.Bundle, err error) error {
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		b.insertSingle(bundle)
	}
	return nil
}

func (b *bundleSet) insertSingle(bundle *fleet.Bundle) {
	key := bundle.Namespace + "/" + bundle.Name
	b.bundleMap[key] = bundle
	b.bundleKeys.Insert(key)
}
