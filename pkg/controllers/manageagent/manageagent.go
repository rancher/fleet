package manageagent

import (
	"context"
	"github.com/sirupsen/logrus"

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
	agentBundleName = "fleet-agent"
)

type handler struct {
	apply           apply.Apply
	systemNamespace string
	clusterCache    fleetcontrollers.ClusterCache
	bundleCache     fleetcontrollers.BundleCache
	namespace corecontrollers.NamespaceController
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
		namespace: namespace,
		apply: apply.
			WithSetID("fleet-manage-agent").
			WithCacheTypes(bundle),
	}

	namespace.OnChange(ctx, "manage-agent", h.OnNamespace)
	relatedresource.WatchClusterScoped(ctx, "manage-agent-resolver", h.resolveNS, namespace, clusters)
}

func (h *handler) resolveNS(namespace, _ string, obj runtime.Object) ([]relatedresource.Key, error) {
	logrus.Infof("[NICK|RESOLVENS] ENTER: %s", namespace)
	if cluster, ok := obj.(*fleet.Cluster); ok {
		if _, err := h.bundleCache.Get(namespace, name.SafeConcatName(agentBundleName, cluster.Name)); err != nil {
			return []relatedresource.Key{{Name: namespace}}, nil
		}
	}
	return nil, nil
}

func (h *handler) OnNamespace(key string, namespace *corev1.Namespace) (*corev1.Namespace, error) {
	opts := metav1.ListOptions{}
	list, err := h.namespace.List(opts)
	if err != nil {
		return nil, err
	}
	for _, ns := range list.Items {
		logrus.Infof("[NICK|ONNAMESPACE] ENTER LIST: %s (%s)", ns.Name, ns.Status.Phase)
	}

	if namespace == nil {
		logrus.Info("[NICK|ONNAMESPACE] NAMESPACE NIL")
		return nil, nil
	}
	logrus.Infof("[NICK|ONNAMESPACE] NAMESPACE: %s", namespace.Name)

	clusters, err := h.clusterCache.List(namespace.Name, labels.Everything())
	if err != nil {
		logrus.Info("[NICK|ONNAMESPACE] LIST FAIL")
		return nil, err
	}

	if len(clusters) == 0 {
		logrus.Infof("[NICK|ONNAMESPACE] LEN NONE: %s", namespace.Name)
		return namespace, nil
	}

	var objs []runtime.Object

	for _, cluster := range clusters {
		bundle, err := h.getAgentBundle(namespace.Name, cluster)
		if err != nil {
			logrus.Infof("[NICK|ONNAMESPACE] GET BUNDLE FAIL: %s (%s) (%s)", namespace.Name, cluster.Namespace, cluster.Name)
			return nil, err
		}
		logrus.Infof("[NICK|ONNAMESPACE] GOT BUNDLE: %s (%s) (%s)", namespace.Name, cluster.Namespace, cluster.Name)
		objs = append(objs, bundle)
	}

	list, err = h.namespace.List(opts)
	if err != nil {
		return nil, err
	}
	for _, ns := range list.Items {
		logrus.Infof("[NICK|ONNAMESPACE] MID LIST: %s (%s)", ns.Name, ns.Status.Phase)
	}

	err = h.apply.
		WithOwner(namespace).
		WithDefaultNamespace(namespace.Name).
		WithListerNamespace(namespace.Name).
		ApplyObjects(objs...)

	list, err = h.namespace.List(opts)
	if err != nil {
		return nil, err
	}
	for _, ns := range list.Items {
		logrus.Infof("[NICK|ONNAMESPACE] (3): %s (%s)", ns.Name, ns.Status.Phase)
	}

	return namespace, err
}

func (h *handler) getAgentBundle(ns string, cluster *fleet.Cluster) (runtime.Object, error) {
	cfg := config.Get()
	if cfg.ManageAgent != nil && !*cfg.ManageAgent {
		return nil, nil
	}

	objs := agent.Manifest(h.systemNamespace, cfg.AgentImage, cfg.AgentImagePullPolicy, "bundle", cfg.AgentCheckinInternal.Duration.String(), cluster.Spec.AgentEnvVars)
	agentYAML, err := yaml.Export(objs...)
	if err != nil {
		return nil, err
	}

	return &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.SafeConcatName(agentBundleName, cluster.Name),
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
