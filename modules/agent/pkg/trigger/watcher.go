// Package trigger watches a set of deployed resources and triggers a callback when one of them is deleted. (fleetagent)
package trigger

import (
	"context"
	"sync"
	"time"

	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/wrangler/pkg/objectset"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type Trigger struct {
	sync.RWMutex

	ctx        context.Context
	objectSets map[string]*objectset.ObjectSet
	watches    map[schema.GroupVersionKind]*watcher
	triggers   map[schema.GroupVersionKind]map[objectset.ObjectKey]map[string]func()
	restMapper meta.RESTMapper
	client     dynamic.Interface
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

func (t *Trigger) call(gvk schema.GroupVersionKind, key objectset.ObjectKey) {
	t.RLock()
	defer t.RUnlock()

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
	resourceVersion := ""
	for {
		w.Lock()
		if w.stopped {
			w.Unlock()
			break
		}
		w.Unlock()

		time.Sleep(durations.TriggerSleep)
		resp, err := w.client.Resource(w.gvr).Watch(ctx, metav1.ListOptions{
			AllowWatchBookmarks: true,
			ResourceVersion:     resourceVersion,
		})
		if err != nil {
			resourceVersion = ""
			continue
		}

		w.Lock()
		w.w = resp
		w.Unlock()

		for event := range resp.ResultChan() {
			meta, err := meta.Accessor(event.Object)
			var key objectset.ObjectKey
			if err == nil {
				resourceVersion = meta.GetResourceVersion()
				key.Name = meta.GetName()
				key.Namespace = meta.GetNamespace()
			}

			switch event.Type {
			case watch.Added:
				fallthrough
			case watch.Modified:
				fallthrough
			case watch.Deleted:
				w.t.call(w.gvk, key)
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
