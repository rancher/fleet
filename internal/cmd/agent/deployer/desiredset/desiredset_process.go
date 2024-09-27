package desiredset

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/merr"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"

	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

func (o *desiredSet) getControllerAndClient(gvk schema.GroupVersionKind) (cache.SharedIndexInformer, dynamic.NamespaceableResourceInterface, error) {
	// client needs to be accessed first so that the gvk->gvr mapping gets cached
	client, err := o.client.clients.client(gvk)
	if err != nil {
		return nil, nil, err
	}

	return o.client.informers[gvk], client, nil
}

func (o *desiredSet) adjustNamespace(objs objectset.ObjectByKey) error {
	for k, v := range objs {
		if k.Namespace != "" {
			continue
		}

		v = v.DeepCopyObject()
		meta, err := meta.Accessor(v)
		if err != nil {
			return err
		}

		meta.SetNamespace(o.defaultNamespace)
		delete(objs, k)
		k.Namespace = o.defaultNamespace
		objs[k] = v
	}

	return nil
}

func (o *desiredSet) clearNamespace(objs objectset.ObjectByKey) error {
	for k, v := range objs {
		if k.Namespace == "" {
			continue
		}

		v = v.DeepCopyObject()
		meta, err := meta.Accessor(v)
		if err != nil {
			return err
		}

		meta.SetNamespace("")

		delete(objs, k)
		k.Namespace = ""
		objs[k] = v
	}

	return nil
}

func (o *desiredSet) filterCrossVersion(gvk schema.GroupVersionKind, keys []objectset.ObjectKey) []objectset.ObjectKey {
	result := make([]objectset.ObjectKey, 0, len(keys))
	gk := gvk.GroupKind()
	for _, key := range keys {
		if o.objs.Contains(gk, key) {
			continue
		}
		if key.Namespace == o.defaultNamespace && o.objs.Contains(gk, objectset.ObjectKey{Name: key.Name}) {
			continue
		}
		result = append(result, key)
	}
	return result
}

func (o *desiredSet) process(ctx context.Context, set labels.Selector, gvk schema.GroupVersionKind, objs objectset.ObjectByKey) {
	controller, client, err := o.getControllerAndClient(gvk)
	if err != nil {
		_ = o.addErr(err)
		return
	}

	nsed, err := o.client.clients.IsNamespaced(gvk)
	if err != nil {
		_ = o.addErr(err)
		return
	}

	if nsed {
		if err := o.adjustNamespace(objs); err != nil {
			_ = o.addErr(err)
			return
		}
	} else {
		if err := o.clearNamespace(objs); err != nil {
			_ = o.addErr(err)
			return
		}
	}

	existing, err := o.list(ctx, controller, client, set, objs)
	if err != nil {
		_ = o.addErr(fmt.Errorf("failed to list %s for %s: %w", gvk, o.setID, err))
		return
	}

	toCreate, toDelete, toUpdate := compareSets(existing, objs)

	// check for resources in the objectset but under a different version of the same group/kind
	toDelete = o.filterCrossVersion(gvk, toDelete)

	o.plan.Create[gvk] = toCreate
	o.plan.Delete[gvk] = toDelete

	// this is not needed for driftdetect.allResources, so PlanDelete exits early
	if o.onlyDelete {
		return
	}

	logger := log.FromContext(ctx).WithValues("setID", o.setID, "gvk", gvk)
	for _, k := range toUpdate {
		oldObject := existing[k]
		newObject := objs[k]

		oldMetadata, err := meta.Accessor(oldObject)
		if err != nil {
			_ = o.addErr(fmt.Errorf("failed to update patch %s for %s, access meta: %w", gvk, o.setID, err))
		}

		o.plan.Objects = append(o.plan.Objects, oldObject)

		logger := logger.WithValues("name", oldMetadata.GetName(), "namespace", oldMetadata.GetNamespace())
		err = o.compareObjects(logger, gvk, oldObject, newObject)
		if err != nil {
			_ = o.addErr(fmt.Errorf("failed to update patch %s for %s: %w", gvk, o.setID, err))
		}
	}
}

func (o *desiredSet) list(ctx context.Context, informer cache.SharedIndexInformer, client dynamic.NamespaceableResourceInterface, selector labels.Selector, desiredObjects objectset.ObjectByKey) (map[objectset.ObjectKey]runtime.Object, error) {
	var (
		errs []error
		objs = objectset.ObjectByKey{}
	)

	if informer == nil {
		// If a lister namespace is set, assume all objects belong to the listerNamespace.  If the
		// desiredSet has an owner but no lister namespace, list objects from all namespaces to ensure
		// we're cleaning up any owned resources.  Otherwise, search only objects from the namespaces
		// used by the objects.  Note: desiredSets without owners will never return objects to delete;
		// deletion requires an owner to track object references across multiple apply runs.
		var namespaces []string = desiredObjects.Namespaces()

		// no owner or lister namespace intentionally restricted; only search in specified namespaces
		err := multiNamespaceList(ctx, namespaces, client, selector, func(obj unstructured.Unstructured) {
			if err := addObjectToMap(objs, &obj); err != nil {
				errs = append(errs, err)
			}
		})
		if err != nil {
			errs = append(errs, err)
		}

		return objs, merr.NewErrors(errs...)
	}

	var namespace string

	// Special case for listing only by hash using indexers
	indexer := informer.GetIndexer()
	if hash, ok := getIndexableHash(indexer, selector); ok {
		return listByHash(indexer, hash, namespace)
	}

	if err := cache.ListAllByNamespace(indexer, namespace, selector, func(obj interface{}) {
		if err := addObjectToMap(objs, obj); err != nil {
			errs = append(errs, err)
		}
	}); err != nil {
		errs = append(errs, err)
	}

	return objs, merr.NewErrors(errs...)
}

func shouldPrune(obj runtime.Object) bool {
	meta, err := meta.Accessor(obj)
	if err != nil {
		return true
	}
	return meta.GetLabels()[LabelPrune] != "false"
}

func compareSets(existingSet, newSet objectset.ObjectByKey) (toCreate, toDelete, toUpdate []objectset.ObjectKey) {
	for k := range newSet {
		if _, ok := existingSet[k]; ok {
			toUpdate = append(toUpdate, k)
		} else {
			toCreate = append(toCreate, k)
		}
	}

	for k, obj := range existingSet {
		if _, ok := newSet[k]; !ok {
			if shouldPrune(obj) {
				toDelete = append(toDelete, k)
			}
		}
	}

	sortObjectKeys(toCreate)
	sortObjectKeys(toDelete)
	sortObjectKeys(toUpdate)

	return
}

func sortObjectKeys(keys []objectset.ObjectKey) {
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})
}

func addObjectToMap(objs objectset.ObjectByKey, obj interface{}) error {
	metadata, err := meta.Accessor(obj)
	if err != nil {
		return err
	}

	objs[objectset.ObjectKey{
		Namespace: metadata.GetNamespace(),
		Name:      metadata.GetName(),
	}] = obj.(runtime.Object)

	return nil
}

// multiNamespaceList lists objects across all given namespaces, because requests are concurrent it is possible for appendFn to be called before errors are reported.
func multiNamespaceList(ctx context.Context, namespaces []string, baseClient dynamic.NamespaceableResourceInterface, selector labels.Selector, appendFn func(obj unstructured.Unstructured)) error {
	var mu sync.Mutex
	wg, _ctx := errgroup.WithContext(ctx)

	// list all namespaces concurrently
	for _, namespace := range namespaces {
		namespace := namespace
		wg.Go(func() error {
			list, err := baseClient.Namespace(namespace).List(_ctx, v1.ListOptions{
				LabelSelector: selector.String(),
			})
			if err != nil {
				return err
			}

			mu.Lock()
			for _, obj := range list.Items {
				appendFn(obj)
			}
			mu.Unlock()

			return nil
		})
	}

	return wg.Wait()
}

// getIndexableHash detects if provided selector can be replaced by using the hash index, if configured, in which case returns the hash value
func getIndexableHash(indexer cache.Indexer, selector labels.Selector) (string, bool) {
	// Check if indexer was added
	if indexer == nil || indexer.GetIndexers()[byHash] == nil {
		return "", false
	}

	// Check specific case of listing with exact hash label selector
	if req, selectable := selector.Requirements(); len(req) != 1 || !selectable {
		return "", false
	}

	return selector.RequiresExactMatch(LabelHash)
}

// inNamespace checks whether a given object is a Kubernetes object and is part of the provided namespace
func inNamespace(namespace string, obj interface{}) bool {
	metadata, err := meta.Accessor(obj)
	return err == nil && metadata.GetNamespace() == namespace
}

// listByHash use a pre-configured indexer to list objects of a certain type by their hash label
func listByHash(indexer cache.Indexer, hash string, namespace string) (map[objectset.ObjectKey]runtime.Object, error) {
	var (
		errs []error
		objs = objectset.ObjectByKey{}
	)
	res, err := indexer.ByIndex(byHash, hash)
	if err != nil {
		return nil, err
	}
	for _, obj := range res {
		if namespace != "" && !inNamespace(namespace, obj) {
			continue
		}
		if err := addObjectToMap(objs, obj); err != nil {
			errs = append(errs, err)
		}
	}
	return objs, merr.NewErrors(errs...)
}
