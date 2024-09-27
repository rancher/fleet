package desiredset

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	defaultNamespace = "default"
)

type ClientFactory func(gvr schema.GroupVersionResource) (dynamic.NamespaceableResourceInterface, error)

type InformerFactory interface {
	Get(gvk schema.GroupVersionKind, gvr schema.GroupVersionResource) (cache.SharedIndexInformer, error)
}

type InformerGetter interface {
	Informer() cache.SharedIndexInformer
	GroupVersionKind() schema.GroupVersionKind
}

func newForConfig(cfg *rest.Config) (*Client, error) {
	discovery, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	cf := newClientFactory(cfg)
	return &Client{
		clients: &clients{
			clientFactory: cf,
			discovery:     discovery,
			namespaced:    map[schema.GroupVersionKind]bool{},
			gvkToGVR:      map[schema.GroupVersionKind]schema.GroupVersionResource{},
			clients:       map[schema.GroupVersionKind]dynamic.NamespaceableResourceInterface{},
		},
		informers: map[schema.GroupVersionKind]cache.SharedIndexInformer{},
	}, nil
}

type Client struct {
	clients   *clients
	informers map[schema.GroupVersionKind]cache.SharedIndexInformer
}

type clients struct {
	sync.Mutex

	clientFactory ClientFactory
	discovery     discovery.DiscoveryInterface
	namespaced    map[schema.GroupVersionKind]bool
	gvkToGVR      map[schema.GroupVersionKind]schema.GroupVersionResource
	clients       map[schema.GroupVersionKind]dynamic.NamespaceableResourceInterface
}

func (c *clients) IsNamespaced(gvk schema.GroupVersionKind) (bool, error) {
	c.Lock()
	ok, exists := c.namespaced[gvk]
	c.Unlock()

	if exists {
		return ok, nil
	}
	_, err := c.client(gvk)
	if err != nil {
		return false, err
	}

	c.Lock()
	defer c.Unlock()
	return c.namespaced[gvk], nil
}

func (c *clients) client(gvk schema.GroupVersionKind) (dynamic.NamespaceableResourceInterface, error) {
	c.Lock()
	defer c.Unlock()

	if client, ok := c.clients[gvk]; ok {
		return client, nil
	}

	resources, err := c.discovery.ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		return nil, err
	}

	for _, resource := range resources.APIResources {
		if resource.Kind != gvk.Kind {
			continue
		}

		client, err := c.clientFactory(gvk.GroupVersion().WithResource(resource.Name))
		if err != nil {
			return nil, err
		}

		c.namespaced[gvk] = resource.Namespaced
		c.clients[gvk] = client
		c.gvkToGVR[gvk] = gvk.GroupVersion().WithResource(resource.Name)
		return client, nil
	}

	return nil, fmt.Errorf("failed to discover client for %s", gvk)
}

func newClientFactory(config *rest.Config) ClientFactory {
	return func(gvr schema.GroupVersionResource) (dynamic.NamespaceableResourceInterface, error) {
		client, err := dynamic.NewForConfig(config)
		if err != nil {
			return nil, err
		}

		return client.Resource(gvr), nil
	}
}
