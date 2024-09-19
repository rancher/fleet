// Package manageagent provides a controller for managing the agent bundle.
//
// Allows Fleet to deploy the Fleet Agent itself as a Bundle, which ensures
// changes to Fleet’s configuration are reflected in the Agent.
// The agent is deployed into the namespace, that contains a cluster resource.
package manageagent

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/relatedresource"
	"github.com/rancher/wrangler/v3/pkg/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
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

	// update the agent bundles for all clusters in the triggered namespace
	namespaces.OnChange(ctx, "manage-agent", h.OnNamespace)
	// enqueue events for the agent bundle's namespace when clusters change
	relatedresource.WatchClusterScoped(ctx, "manage-agent-resolver", h.resolveNS, namespaces, clusters)
	fleetcontrollers.RegisterClusterStatusHandler(ctx,
		clusters,
		"Reconciled",
		"agent-env-vars",
		h.onClusterStatusChange)
}

func (h *handler) onClusterStatusChange(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, error) {
	logrus.Debugf("Reconciling agent settings for cluster %s/%s", cluster.Namespace, cluster.Name)

	status, vars, err := h.reconcileAgentEnvVars(cluster, status)
	if err != nil {
		return status, err
	}

	status, changed, err := h.updateClusterStatus(cluster, status)
	if err != nil {
		return status, err
	}

	if vars || changed {
		// trigger importCluster to re-create the deployment, in case
		// the agent cannot update itself from the bundle
		status.AgentConfigChanged = true
		h.namespaces.Enqueue(cluster.Namespace)
	}

	return status, nil
}

func hashStatusField(field any) (string, error) {
	hasher := sha256.New224()
	b, err := json.Marshal(field)
	if err != nil {
		return "", err
	}
	hasher.Write(b)
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func hashChanged(field any, statusHash string) (bool, string, error) {
	isNil := func(field any) bool {
		switch field := field.(type) {
		case *corev1.Affinity:
			return field == nil
		case *corev1.ResourceRequirements:
			return field == nil
		case []corev1.Toleration:
			return len(field) == 0
		default:
			return false
		}
	}

	if isNil(field) {
		if statusHash != "" {
			return true, "", nil
		}
		return false, "", nil
	}

	hash, err := hashStatusField(field)
	if err != nil {
		return false, "", err
	}

	return statusHash != hash, hash, nil
}

// reconcileAgentEnvVars checks if the agent environment variables field was
// updated by hashing its contents into a status field.
func (h *handler) reconcileAgentEnvVars(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, bool, error) {
	enqueue := false

	if len(cluster.Spec.AgentEnvVars) < 1 {
		// Remove the existing hash if the environment variables have been deleted.
		if status.AgentEnvVarsHash != "" {
			// We enqueue to ensure that we edit the status after other controllers.
			enqueue = true
			status.AgentEnvVarsHash = ""
		}
		return status, enqueue, nil
	}

	hash, err := hashStatusField(cluster.Spec.AgentEnvVars)
	if err != nil {
		return status, enqueue, err
	}

	if status.AgentEnvVarsHash != hash {
		// We enqueue to ensure that we edit the status after other controllers.
		enqueue = true
		status.AgentEnvVarsHash = hash
	}

	return status, enqueue, nil
}

func (h *handler) updateClusterStatus(cluster *fleet.Cluster, status fleet.ClusterStatus) (fleet.ClusterStatus, bool, error) {
	changed := false

	if status.AgentPrivateRepoURL != cluster.Spec.PrivateRepoURL {
		status.AgentPrivateRepoURL = cluster.Spec.PrivateRepoURL
		changed = true
	}

	if hostNetwork := *cmp.Or(cluster.Spec.HostNetwork, ptr.To(false)); status.AgentHostNetwork != hostNetwork {
		status.AgentHostNetwork = hostNetwork
		changed = true
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentAffinity, status.AgentAffinityHash); err != nil {
		return status, changed, err
	} else if c {
		status.AgentAffinityHash = hash
		changed = c
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentResources, status.AgentResourcesHash); err != nil {
		return status, changed, err
	} else if c {
		status.AgentResourcesHash = hash
		changed = c
	}

	if c, hash, err := hashChanged(cluster.Spec.AgentTolerations, status.AgentTolerationsHash); err != nil {
		return status, changed, err
	} else if c {
		status.AgentTolerationsHash = hash
		changed = c
	}

	return status, changed, nil
}

func (h *handler) resolveNS(namespace, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	if cluster, ok := obj.(*fleet.Cluster); ok {
		if _, err := h.bundleCache.Get(namespace, names.SafeConcatName(AgentBundleName, cluster.Name)); err != nil {
			return []relatedresource.Key{{Name: namespace}}, nil
		}
	}
	return nil, nil
}

// OnNamespace updates agent bundles for all clusters in the namespace
func (h *handler) OnNamespace(key string, namespace *corev1.Namespace) (*corev1.Namespace, error) {
	if namespace == nil {
		return nil, nil
	}

	cfg := config.Get()
	// managed agents are disabled, so we don't need to create the bundle
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
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
		logrus.Infof("Update agent bundle for cluster %s/%s", cluster.Namespace, cluster.Name)
		bundle, err := h.newAgentBundle(namespace.Name, cluster)
		if err != nil {
			logrus.Errorf("Failed to update agent bundle for cluster %s/%s", cluster.Namespace, cluster.Name)
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

func (h *handler) newAgentBundle(ns string, cluster *fleet.Cluster) (runtime.Object, error) {
	cfg := config.Get()
	agentNamespace := h.systemNamespace
	if cluster.Spec.AgentNamespace != "" {
		agentNamespace = cluster.Spec.AgentNamespace
	}

	// Notice we only set the agentScope when it's a non-default agentNamespace. This is for backwards compatibility
	// for when we didn't have agent scope before
	objs := agent.Manifest(
		agentNamespace, cluster.Spec.AgentNamespace,
		agent.ManifestOptions{
			AgentEnvVars:          cluster.Spec.AgentEnvVars,
			AgentImage:            cfg.AgentImage,
			AgentImagePullPolicy:  cfg.AgentImagePullPolicy,
			AgentTolerations:      cluster.Spec.AgentTolerations,
			CheckinInterval:       cfg.AgentCheckinInterval.Duration.String(),
			PrivateRepoURL:        cluster.Spec.PrivateRepoURL,
			SystemDefaultRegistry: cfg.SystemDefaultRegistry,
			AgentAffinity:         cluster.Spec.AgentAffinity,
			AgentResources:        cluster.Spec.AgentResources,
			HostNetwork:           *cmp.Or(cluster.Spec.HostNetwork, ptr.To(false)),
		},
	)
	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}

	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SafeConcatName(AgentBundleName, cluster.Name),
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
