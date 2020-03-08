package client

import (
	"fmt"

	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	corev1 "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/kubeconfig"
)

type Getter struct {
	Kubeconfig      string
	FleetKubeconfig string
	Namespace       string
}

func (g *Getter) Get() (*Client, error) {
	if g == nil {
		return nil, fmt.Errorf("client is not configured, please set client getter")
	}
	return NewClient(g.Kubeconfig, g.Namespace)
}

func (g *Getter) GetFleet() (*Client, error) {
	if g == nil {
		return nil, fmt.Errorf("client is not configured, please set client getter")
	}
	kubeconfig := g.FleetKubeconfig
	if kubeconfig == "" {
		kubeconfig = g.Kubeconfig
	}
	return NewClient(kubeconfig, g.Namespace)
}

type Client struct {
	Fleet     fleetcontrollers.Interface
	Core      corev1.Interface
	Apply     apply.Apply
	Namespace string
}

func NewGetter(kubeconfig, namespace string) *Getter {
	return &Getter{
		Kubeconfig: kubeconfig,
		Namespace:  namespace,
	}
}

func NewClient(kubeConfig, namespace string) (*Client, error) {
	cc := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	ns, _, err := cc.Namespace()
	if err != nil {
		return nil, err
	}

	if namespace != "" {
		ns = namespace
	}

	c := &Client{
		Namespace: ns,
	}

	restConfig, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}

	fleet, err := fleet.NewFactoryFromConfig(restConfig)
	if err != nil {
		return nil, err
	}
	c.Fleet = fleet.Fleet().V1alpha1()

	core, err := core.NewFactoryFromConfig(restConfig)
	if err != nil {
		return nil, err
	}
	c.Core = core.Core().V1()

	c.Apply, err = apply.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	if c.Namespace == "" {
		c.Namespace = "default"
	}

	c.Apply = c.Apply.
		WithDynamicLookup().
		WithDefaultNamespace(c.Namespace).
		WithListerNamespace(c.Namespace).
		WithRestrictClusterScoped()

	return c, nil
}
