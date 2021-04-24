package manageagent

import (
	"context"
	"fmt"
	"sort"

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
	kyaml "sigs.k8s.io/yaml"
)

const (
	agentBundleName = "fleet-agent"
)

type handler struct {
	apply           apply.Apply
	systemNamespace string
	clusterCache    fleetcontrollers.ClusterCache
	bundleCache     fleetcontrollers.BundleCache
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
		bundleCache:     bundle.Cache(),
		apply: apply.
			WithSetID("fleet-manage-agent").
			WithCacheTypes(bundle),
	}

	namespace.OnChange(ctx, "manage-agent", h.OnNamespace)
	relatedresource.WatchClusterScoped(ctx, "manage-agent-resolver", h.resolveNS, namespace, clusters)
}

func (h *handler) resolveNS(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if _, ok := obj.(*fleet.Cluster); ok {
		if _, err := h.bundleCache.Get(namespace, agentBundleName); err == nil {
			return []relatedresource.Key{{Name: namespace}}, nil
		}
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

	var objs []runtime.Object
	bundle, err := h.getAgentBundle(namespace.Name, clusters)
	if err != nil {
		return nil, err
	}
	objs = append(objs, bundle)

	return namespace, h.apply.
		WithOwner(namespace).
		WithDefaultNamespace(namespace.Name).
		WithListerNamespace(namespace.Name).
		ApplyObjects(objs...)
}

func (h *handler) getAgentBundle(ns string, clusters []*fleet.Cluster) (*fleet.Bundle, error) {
	cfg := config.Get()
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
		return nil, nil
	}

	objs := agent.Manifest(h.systemNamespace, cfg.AgentImage, cfg.AgentImagePullPolicy, "bundle", cfg.AgentCheckinInternal.Duration.String(), nil)
	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}
	kustomizeBase := map[string][]string{
		"resources": {"agent.yaml"},
	}
	kustomizeBaseBytes, err := kyaml.Marshal(kustomizeBase)
	if err != nil {
		return nil, err
	}

	bundle := &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentBundleName,
			Namespace: ns,
		},
		Spec: fleet.BundleSpec{
			BundleDeploymentOptions: fleet.BundleDeploymentOptions{
				DefaultNamespace: h.systemNamespace,
				Helm: &fleet.HelmOptions{
					TakeOwnership: true,
				},
			},
			Resources: []fleet.BundleResource{
				{
					Name:    "base/agent.yaml",
					Content: string(agentYAML),
				},
				{
					Name:    "base/kustomization.yaml",
					Content: string(kustomizeBaseBytes),
				},
			},
		},
	}

	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Name < clusters[j].Name
	})

	for _, cluster := range clusters {
		target := fleet.BundleTarget{
			ClusterSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "fleet.cattle.io/non-managed-agent",
						Operator: metav1.LabelSelectorOpDoesNotExist,
					},
				},
			},
			ClusterName: cluster.Name,
		}
		target.Kustomize = &fleet.KustomizeOptions{
			Dir: cluster.Name,
		}

		// set up kustomize to add env patch per cluster
		kustomizeFile := map[string][]string{
			"resources": {"../base"},
			"patches":   {"patch.yaml"},
		}
		kustomizeFileBytes, err := kyaml.Marshal(kustomizeFile)
		if err != nil {
			return nil, err
		}

		containerPatch := map[string]interface{}{
			"name": agent.DefaultName,
			"env":  cluster.Spec.AgentEnvVars,
		}
		if len(cluster.Spec.AgentEnvVars) == 0 {
			delete(containerPatch, "env")
		}
		patch := map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]string{
				"name":      agent.DefaultName,
				"namespace": h.systemNamespace,
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []map[string]interface{}{
							containerPatch,
						},
					},
				},
			},
		}
		patchBytes, err := kyaml.Marshal(patch)
		if err != nil {
			return nil, err
		}
		bundle.Spec.Resources = append(bundle.Spec.Resources, fleet.BundleResource{
			Name:    fmt.Sprintf("%s/kustomization.yaml", cluster.Name),
			Content: string(kustomizeFileBytes),
		}, fleet.BundleResource{
			Name:    fmt.Sprintf("%s/patch.yaml", cluster.Name),
			Content: string(patchBytes),
		})

		bundle.Spec.Targets = append(bundle.Spec.Targets, target)
	}

	return bundle, nil
}
