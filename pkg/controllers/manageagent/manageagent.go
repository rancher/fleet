package manageagent

import (
	"context"

	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/yaml"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/fleet/pkg/agent"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	agentBundleName = "fleet-agent"
)

type handler struct {
	apply           apply.Apply
	systemNamespace string
	clusterGroup    fleetcontrollers.ClusterGroupController
	bundleCache     fleetcontrollers.BundleCache
}

func Register(ctx context.Context,
	systemNamespace string,
	apply apply.Apply,
	clusterGroup fleetcontrollers.ClusterGroupController,
	bundle fleetcontrollers.BundleController,
) {
	h := handler{
		systemNamespace: systemNamespace,
		clusterGroup:    clusterGroup,
		bundleCache:     bundle.Cache(),
		apply: apply.
			WithSetID("fleet-global-config").
			WithCacheTypes(bundle),
	}

	config.OnChange(ctx, h.OnConfig)
	clusterGroup.OnChange(ctx, "manage-agent", h.OnClusterGroup)
}

func (h *handler) OnClusterGroup(key string, clusterGroup *fleet.ClusterGroup) (*fleet.ClusterGroup, error) {
	if clusterGroup == nil {
		return nil, nil
	}

	objs, err := h.getAgentBundle(clusterGroup.Namespace)
	if err != nil {
		return nil, err
	}

	return clusterGroup, h.apply.
		WithSetID(name.SafeConcatName("fleet-global-config", clusterGroup.Namespace)).
		ApplyObjects(objs...)
}

func (h *handler) getAgentBundle(ns string) ([]runtime.Object, error) {
	cfg := config.Get()
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
		return nil, nil
	}

	objs := agent.Manifest(h.systemNamespace, cfg.AgentImage)
	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}

	return []runtime.Object{
		&fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      agentBundleName,
				Namespace: ns,
				Labels:    map[string]string{"a": "b"},
			},
			Spec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{
					DefaultNamespace: h.systemNamespace,
					TimeoutSeconds:   5,
				},
				Resources: []fleet.BundleResource{
					{
						Name:    "agent.yaml",
						Content: string(agentYAML),
					},
				},
				Targets: []fleet.BundleTarget{
					{
						ClusterSelector: &metav1.LabelSelector{},
					},
				},
			},
		},
	}, nil
}

func (h *handler) OnConfig(config *config.Config) error {
	cgs, err := h.clusterGroup.Cache().List("", labels.Everything())
	if err != nil {
		return err
	}

	nsSeen := map[string]bool{}
	for _, cg := range cgs {
		if nsSeen[cg.Namespace] {
			continue
		}
		nsSeen[cg.Namespace] = true
		h.clusterGroup.Enqueue(cg.Namespace, cg.Name)
	}

	return nil
}

func objects(systemNamespace string, config *config.Config) []runtime.Object {
	if config.ManageAgent != nil && !*config.ManageAgent {
		return nil
	}

	return agent.Manifest(systemNamespace, config.AgentImage)
}
