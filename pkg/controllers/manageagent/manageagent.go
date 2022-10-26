package manageagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/rancher/fleet/pkg/agent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/rancher/wrangler/pkg/yaml"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AgentBundleName = "fleet-agent"
)

type handler struct {
	apply           apply.Apply
	systemNamespace string
	clusterCache    fleetcontrollers.ClusterCache
	bundleCache     fleetcontrollers.BundleCache
	namespaces      corecontrollers.NamespaceController
}

func Register(ctx context.Context,
	systemNamespace string,
	apply apply.Apply,
	namespaces corecontrollers.NamespaceController,
	clusters fleetcontrollers.ClusterController,
	bundle fleetcontrollers.BundleController,
) {
	h := handler{
		systemNamespace: systemNamespace,
		clusterCache:    clusters.Cache(),
		bundleCache:     bundle.Cache(),
		namespaces:      namespaces,
		apply: apply.
			WithSetID("fleet-manage-agent").
			WithCacheTypes(bundle),
	}

	namespaces.OnChange(ctx, "manage-agent", h.OnNamespace)
	relatedresource.WatchClusterScoped(ctx, "manage-agent-resolver", h.resolveNS, namespaces, clusters)
	fleetcontrollers.RegisterClusterStatusHandler(ctx,
		clusters,
		"Reconciled",
		"agent-env-vars",
		h.OnClusterChange)
}

func (h *handler) OnClusterChange(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	// Check if the agent environment variables field was updated by hashing its contents into a status field.
	return h.reconcileAgentEnvVars(cluster, status)
}

func (h *handler) reconcileAgentEnvVars(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	if len(cluster.Spec.AgentEnvVars) < 1 {
		// Remove the existing hash if the environment variables have been deleted.
		if status.AgentEnvVarsHash != "" {
			// We enqueue to ensure that we edit the status after other controllers.
			h.namespaces.Enqueue(cluster.Namespace)
			status.AgentEnvVarsHash = ""
		}
		return status, nil
	}

	hasher := sha256.New224()
	b, err := json.Marshal(cluster.Spec.AgentEnvVars)
	if err != nil {
		return status, err
	}
	hasher.Write(b)
	hash := fmt.Sprintf("%x", hasher.Sum(nil))

	if status.AgentEnvVarsHash != hash {
		// We enqueue to ensure that we edit the status after other controllers.
		h.namespaces.Enqueue(cluster.Namespace)
		status.AgentEnvVarsHash = hash
	}
	return status, nil
}

func (h *handler) resolveNS(namespace, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if cluster, ok := obj.(*fleet.Cluster); ok {
		if _, err := h.bundleCache.Get(namespace, name.SafeConcatName(AgentBundleName, cluster.Name)); err != nil {
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

	for _, cluster := range clusters {
		bundle, err := h.getAgentBundle(namespace.Name, cluster)
		if err != nil {
			return nil, err
		}
		objs = append(objs, bundle)
	}

	return namespace, h.apply.
		WithOwner(namespace).
		WithDefaultNamespace(namespace.Name).
		WithListerNamespace(namespace.Name).
		ApplyObjects(objs...)
}

func (h *handler) getAgentBundle(ns string, cluster *fleet.Cluster) (runtime.Object, error) {
	cfg := config.Get()
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
		return nil, nil
	}

	agentNamespace := h.systemNamespace
	if cluster.Spec.AgentNamespace != "" {
		agentNamespace = cluster.Spec.AgentNamespace
	}

	// Notice we only set the agentScope when it's a non-default agentNamespace. This is for backwards compatibility
	// for when we didn't have agent scope before
	objs := agent.Manifest(agentNamespace, cluster.Spec.AgentNamespace, cfg.AgentImage, cfg.AgentImagePullPolicy, "bundle", cfg.AgentCheckinInterval.Duration.String(), cluster.Spec.AgentEnvVars)

	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}

	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.SafeConcatName(AgentBundleName, cluster.Name),
			Namespace: ns,
		},
		Spec: fleet.BundleSpec{
			BundleDeploymentOptions: fleet.BundleDeploymentOptions{
				DefaultNamespace: agentNamespace,
				Helm: &fleet.HelmOptions{
					TakeOwnership: true,
				},
			},
			Resources: []fleet.BundleResource{
				{
					Name:    "agent.yaml",
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
					ClusterName: cluster.Name,
				},
			},
		},
	}, nil
}
