package target

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corecontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
)

type BundleMapping struct {
	namespace         string
	namespaceSelector labels.Selector
	bundleSelector    labels.Selector
	namespaces        corecontrollers.NamespaceCache
	bundles           fleetcontrollers.BundleCache
	noMatch           bool
}

func NewBundleMapping(mapping *fleet.BundleNamespaceMapping,
	namespaces corecontrollers.NamespaceCache,
	bundles fleetcontrollers.BundleCache) (*BundleMapping, error) {
	var (
		result = &BundleMapping{
			namespace:  mapping.Namespace,
			namespaces: namespaces,
			bundles:    bundles,
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

func (b *BundleMapping) Bundles() ([]*fleet.Bundle, error) {
	if b.noMatch {
		return nil, nil
	}
	return b.bundles.List(b.namespace, b.bundleSelector)
}

func (b *BundleMapping) MatchesNamespace(namespace string) bool {
	if b.noMatch {
		return false
	}
	ns, err := b.namespaces.Get(namespace)
	if err != nil {
		return false
	}
	return b.namespaceSelector.Matches(labels.Set(ns.Labels))
}

func (b *BundleMapping) Matches(fleetBundle *fleet.Bundle) bool {
	if b.noMatch {
		return false
	}
	if fleetBundle.Namespace != b.namespace {
		return false
	}
	return b.bundleSelector.Matches(labels.Set(fleetBundle.Labels))
}

func (b *BundleMapping) Namespaces() ([]*corev1.Namespace, error) {
	if b.noMatch {
		return nil, nil
	}
	return b.namespaces.List(b.namespaceSelector)
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
