// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"sync"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectCache stores previous versions of objects for comparison
type ObjectCache struct {
	mu    sync.RWMutex
	cache map[types.NamespacedName]client.Object
}

// NewObjectCache creates a new ObjectCache
func NewObjectCache() *ObjectCache {
	return &ObjectCache{
		cache: make(map[types.NamespacedName]client.Object),
	}
}

// Get retrieves an object from the cache
func (c *ObjectCache) Get(key types.NamespacedName) (client.Object, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	obj, exists := c.cache[key]
	return obj, exists
}

// Set stores an object in the cache
func (c *ObjectCache) Set(key types.NamespacedName, obj client.Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = obj
}

// Delete removes an object from the cache
func (c *ObjectCache) Delete(key types.NamespacedName) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, key)
}
