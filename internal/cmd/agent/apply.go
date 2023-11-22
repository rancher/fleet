package agent

import (
	"context"
	"time"

	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// LocalClients returns an apply.Apply. It is public so that tests don't have to replicate the setup.
func LocalClients(ctx context.Context, localconfig *rest.Config) (apply.Apply, meta.RESTMapper, *dynamic.DynamicClient, error) {
	d, err := discovery.NewDiscoveryClientForConfig(localconfig)
	if err != nil {
		return nil, nil, nil, err
	}

	disc := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)

	go func() {
		for range ticker.Context(ctx, 30*time.Second) {
			disc.Invalidate()
			mapper.Reset()
		}
	}()

	dyn, err := dynamic.NewForConfig(localconfig)
	if err != nil {
		return nil, nil, nil, err
	}

	apply, err := apply.NewForConfig(localconfig)
	if err != nil {
		return nil, nil, nil, err
	}
	apply = apply.WithDynamicLookup()

	return apply, mapper, dyn, err
}
