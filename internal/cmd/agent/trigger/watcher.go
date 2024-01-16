// Package trigger watches a set of deployed resources and triggers a callback when one of them is deleted.
package trigger

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rancher/wrangler/v2/pkg/objectset"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"

	"github.com/rancher/fleet/pkg/durations"
)

type Trigger struct {
	sync.RWMutex

	ctx        context.Context
	objectSets map[string]*objectset.ObjectSet
	watches    map[schema.GroupVersionKind]*watcher
	triggers   map[schema.GroupVersionKind]map[objectset.ObjectKey]map[string]func()
	restMapper meta.RESTMapper
	client     dynamic.Interface

	// seenGenerations keeps a registry of the object UIDs and the latest observed generation, if any
	// Uses sync.Map for a safe concurrent usage.
	// Uses atomic.Int64 as values in order to stick to the first use case described at https://pkg.go.dev/sync#Map
	seenGenerations sync.Map
}

func New(ctx context.Context, restMapper meta.RESTMapper, client dynamic.Interface) *Trigger {
	return &Trigger{
		ctx:        ctx,
		objectSets: map[string]*objectset.ObjectSet{},
		watches:    map[schema.GroupVersionKind]*watcher{},
		triggers:   map[schema.GroupVersionKind]map[objectset.ObjectKey]map[string]func(){},
		restMapper: restMapper,
		client:     client,
	}
}

func (t *Trigger) gvr(gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool, error) {
	mapping, err := t.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return schema.GroupVersionResource{}, false, err
	}
	return mapping.Resource, mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
}

func (t *Trigger) Clear(key string) error {
	return t.OnChange(key, "", nil)
}

func setNamespace(nsed bool, key objectset.ObjectKey, defaultNamespace string) objectset.ObjectKey {
	if nsed {
		if key.Namespace == "" {
			key.Namespace = defaultNamespace
		}
	} else {
		key.Namespace = ""
	}
	return key
}

func (t *Trigger) OnChange(key string, defaultNamespace string, trigger func(), objs ...runtime.Object) error {
	t.Lock()
	defer t.Unlock()

	os := objectset.NewObjectSet(objs...)
	oldOS := t.objectSets[key]
	gvkNSed := map[schema.GroupVersionKind]bool{}

	for gvk := range os.ObjectsByGVK() {
		gvr, nsed, err := t.gvr(gvk)
		if err != nil {
			return err
		}
		gvkNSed[gvk] = nsed
		t.watch(gvk, gvr)
	}

	for gvk, objs := range oldOS.ObjectsByGVK() {
		t.unwatch(gvk)
		for objectKey := range objs {
			objectKey = setNamespace(gvkNSed[gvk], objectKey, defaultNamespace)
			delete(t.triggers[gvk][objectKey], key)
		}
	}

	for gvk, objs := range os.ObjectsByGVK() {
		for objectKey := range objs {
			objectKey = setNamespace(gvkNSed[gvk], objectKey, defaultNamespace)
			objectKeys, ok := t.triggers[gvk]
			if !ok {
				objectKeys = map[objectset.ObjectKey]map[string]func(){}
				t.triggers[gvk] = objectKeys
			}
			funcs, ok := objectKeys[objectKey]
			if !ok {
				funcs = map[string]func(){}
				objectKeys[objectKey] = funcs
			}
			funcs[key] = trigger
		}
	}

	// prune
	for k, v := range t.triggers {
		for k, v2 := range v {
			if len(v2) == 0 {
				delete(v, k)
			}
		}
		if len(v) == 0 {
			delete(t.triggers, k)
		}
	}

	if len(objs) == 0 {
		delete(t.objectSets, key)
	} else {
		t.objectSets[key] = os
	}

	return nil
}

func (t *Trigger) storeObjectGeneration(uid types.UID, generation int64) *atomic.Int64 {
	value := new(atomic.Int64)
	value.Store(generation)
	t.seenGenerations.Store(uid, value)
	return value
}

func (t *Trigger) call(gvk schema.GroupVersionKind, obj metav1.Object, deleted bool) {
	// If this type populates Generation metadata, use it to filter events that didn't modify that field
	if currentGeneration := obj.GetGeneration(); currentGeneration != 0 {
		uid := obj.GetUID()
		// if the object is being deleted, just forget about it and execute the callback
		if deleted {
			t.seenGenerations.Delete(uid)
		} else {
			// keep a map of UID -> generation, using sync.Map and atomic.Int64 for safe concurrent usage
			// - sync.Map entries are never modified after created, a pointer is used as value
			// - using atomic.Int64 as values allows safely comparing and updating the current Generation value
			var previous *atomic.Int64
			if value, ok := t.seenGenerations.Load(uid); ok {
				previous = value.(*atomic.Int64)
			} else {
				previous = t.storeObjectGeneration(uid, currentGeneration)
			}

			// Set current generation and retrieve the previous value. if unchanged, do nothing and return early
			if previousGeneration := previous.Swap(currentGeneration); previousGeneration == currentGeneration {
				return
			}
		}
	}

	t.RLock()
	defer t.RUnlock()

	key := objectset.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	for _, f := range t.triggers[gvk][key] {
		f()
	}
}

func (t *Trigger) watch(gvk schema.GroupVersionKind, gvr schema.GroupVersionResource) {
	gvkWatcher, ok := t.watches[gvk]
	if ok {
		gvkWatcher.count++
	} else {
		gvkWatcher = &watcher{
			client: t.client,
			t:      t,
			gvr:    gvr,
			gvk:    gvk,
			count:  1,
		}
		go gvkWatcher.Start(t.ctx)
		t.watches[gvk] = gvkWatcher
	}
}

func (t *Trigger) unwatch(gvk schema.GroupVersionKind) {
	gvkWatcher, ok := t.watches[gvk]
	if !ok {
		return
	}
	gvkWatcher.count--
	if gvkWatcher.count <= 0 {
		gvkWatcher.Stop()
		delete(t.watches, gvk)
	}
}

type watcher struct {
	sync.Mutex

	client  dynamic.Interface
	gvk     schema.GroupVersionKind
	gvr     schema.GroupVersionResource
	count   int
	stopped bool
	w       watch.Interface
	t       *Trigger
}

func (w *watcher) Start(ctx context.Context) {
	// resourceVersion is used as a checkpoint if the Watch operation is interrupted.
	// the for loop will resume watching with a non-empty resource version to avoid missing or repeating events
	resourceVersion := ""
	for {
		w.Lock()
		if w.stopped {
			// The Watch operation was intentionally stopped, exit the loop
			w.Unlock()
			return
		}
		w.Unlock()

		// Watch is non-blocking, the response allows consuming the events or stopping
		// An error may mean the connection could not be established for some reason
		resp, err := w.client.Resource(w.gvr).Watch(ctx, metav1.ListOptions{
			AllowWatchBookmarks: true,
			ResourceVersion:     resourceVersion,
		})
		if err != nil {
			resourceVersion = ""
			time.Sleep(durations.WatchErrorRetrySleep)
			continue
		}

		w.Lock()
		w.w = resp
		w.Unlock()

		for event := range resp.ResultChan() {
			// Not all events include a Kubernetes object payload (see the event.Event godoc), filter those out.
			obj, err := meta.Accessor(event.Object)
			if err != nil {
				continue
			}

			// Store resource version for later resuming if watching is interrupted
			resourceVersion = obj.GetResourceVersion()

			switch event.Type {
			// Just initialize the seen generations.
			case watch.Added:
				if generation := obj.GetGeneration(); generation != 0 {
					w.t.storeObjectGeneration(obj.GetUID(), generation)
				}
			// Only trigger for Modified or Deleted objects.
			case watch.Modified, watch.Deleted:
				deleted := event.Type == watch.Deleted
				w.t.call(w.gvk, obj, deleted)
			}
		}
	}
}

func (w *watcher) Stop() {
	w.Lock()
	defer w.Unlock()
	w.stopped = true
	if w.w != nil {
		w.w.Stop()
	}
}
