package manageagent

import (
	"context"

	"github.com/rancher/fleet/pkg/agent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/rancher/wrangler/pkg/yaml"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	agentBundleName = "fleet-agent"
)

type handler struct {
	apply           apply.Apply
	systemNamespace string
	clusterCache    fleetcontrollers.ClusterCache
}

func Register(ctx context.Context,
	systemNamespace string,
	apply apply.Apply,
	namespace corecontrollers.NamespaceController,
	clusters fleetcontrollers.ClusterController,
	bundle fleetcontrollers.BundleController,
) {
	h := handler{
		systemNamespace: systemNamespace,
		clusterCache:    clusters.Cache(),
		apply: apply.
			WithSetID("fleet-manage-agent").
			WithCacheTypes(bundle),
	}

	namespace.OnChange(ctx, "manage-agent", h.OnNamespace)
	relatedresource.WatchClusterScoped(ctx, "manage-agent-resolver", h.resolveNS, namespace, clusters)
}

func (h *handler) resolveNS(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if _, ok := obj.(*fleet.Cluster); ok {
		return []relatedresource.Key{{Name: namespace}}, nil
	}
	return nil, nil
}

func (h *handler) OnNamespace(key string, namespace *corev1.Namespace) (*corev1.Namespace, error) {
	if namespace == nil {
		return nil, nil
	}

	clusters, err := h.clusterCache.List(namespace.Name, labels.Everything())
	if err != nil {
		return nil, err
	}

	if len(clusters) == 0 {
		return namespace, nil
	}

	objs, err := h.getAgentBundle(namespace.Name)
	if err != nil {
		return nil, err
	}

	return namespace, h.apply.
		WithDefaultNamespace(namespace.Name).
		WithListerNamespace(namespace.Name).
		ApplyObjects(objs...)
}

func (h *handler) getAgentBundle(ns string) ([]runtime.Object, error) {
	cfg := config.Get()
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
		return nil, nil
	}

	objs := agent.Manifest(h.systemNamespace, cfg.AgentImage, cfg.AgentImagePullPolicy)
	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}

	return []runtime.Object{
		&fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      agentBundleName,
				Namespace: ns,
			},
			Spec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{
					DefaultNamespace: h.systemNamespace,
					TakeOwnership:    true,
				},
				Resources: []fleet.BundleResource{
					{
						Content: string(agentYAML),
					},
				},
				Targets: []fleet.BundleTarget{
					{
						ClusterSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "fleet.cattle.io/non-managed-agent",
									Operator: metav1.LabelSelectorOpDoesNotExist,
								},
							},
						},
					},
				},
			},
		},
	}, nil
}
