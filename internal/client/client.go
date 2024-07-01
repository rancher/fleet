package client

import (
	"fmt"

	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/apply"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	corev1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
)

type Getter struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

func (g *Getter) Get() (*Client, error) {
	if g == nil {
		return nil, fmt.Errorf("client is not configured, please set client getter")
	}
	return newClient(g.Kubeconfig, g.Context, g.Namespace)
}

func (g *Getter) GetNamespace() string {
	return g.Namespace
}

type Client struct {
	Fleet     fleetcontrollers.Interface
	Core      corev1.Interface
	RBAC      rbaccontrollers.Interface
	Apply     apply.Apply
	Namespace string
}

func NewGetter(kubeconfig, context, namespace string) *Getter {
	return &Getter{
		Kubeconfig: kubeconfig,
		Context:    context,
		Namespace:  namespace,
	}
}

func newClient(kubeConfig, context, namespace string) (*Client, error) {
	cc := kubeconfig.GetNonInteractiveClientConfigWithContext(kubeConfig, context)
	ns, _, err := cc.Namespace()
	if err != nil {
		return nil, err
	}

	if namespace != "" {
		ns = namespace
	}

	restConfig, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}

	c := &Client{
		Namespace: ns,
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

	rbac, err := rbac.NewFactoryFromConfig(restConfig)
	if err != nil {
		return nil, err
	}
	c.RBAC = rbac.Rbac().V1()

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
