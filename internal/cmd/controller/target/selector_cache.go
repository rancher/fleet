package target

import (
	"sync"

	"k8s.io/apimachinery/pkg/labels"
)

// SelectorCache stores compiled ClusterGroup selectors, as namespace -> name -> entry.
//
// Grouping by namespace means removing stale entries never scans other namespaces. Each
// ClusterGroup has one entry holding its ResourceVersion, so an update overwrites the entry
// instead of leaving the old version behind.
type SelectorCache struct {
	mut        sync.RWMutex
	namespaces map[string]map[string]cachedSelector
}

type cachedSelector struct {
	resourceVersion string
	selector        labels.Selector
}

// Get returns false if the cached entry was compiled from a different resourceVersion.
func (c *SelectorCache) Get(namespace, name, resourceVersion string) (labels.Selector, bool) {
	if c == nil {
		return nil, false
	}
	c.mut.RLock()
	defer c.mut.RUnlock()
	gc, ok := c.namespaces[namespace][name]
	if !ok || gc.resourceVersion != resourceVersion {
		return nil, false
	}
	return gc.selector, true
}

func (c *SelectorCache) Set(namespace, name, resourceVersion string, selector labels.Selector) {
	if c == nil {
		return
	}

	c.mut.Lock()
	defer c.mut.Unlock()

	if c.namespaces == nil {
		c.namespaces = make(map[string]map[string]cachedSelector)
	}
	if c.namespaces[namespace] == nil {
		c.namespaces[namespace] = make(map[string]cachedSelector)
	}
	c.namespaces[namespace][name] = cachedSelector{
		resourceVersion: resourceVersion,
		selector:        selector,
	}
}

// Retain removes the entries in namespace not in keep.
func (c *SelectorCache) Retain(namespace string, keep map[string]struct{}) {
	if c == nil {
		return
	}

	c.mut.Lock()
	defer c.mut.Unlock()
	for name := range c.namespaces[namespace] {
		if _, ok := keep[name]; !ok {
			delete(c.namespaces[namespace], name)
		}
	}
	if len(c.namespaces[namespace]) == 0 {
		delete(c.namespaces, namespace)
	}
}
