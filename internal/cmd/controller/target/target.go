// Package target provides functionality around building and deploying bundledeployments.
//
// Each "Target" represents a bundle, cluster pair and will be transformed into a bundledeployment.
// The manifest, persisted in the content resource, contains the resources available to
// these bundledeployments.
package target

import (
	"strings"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	// Default limit is 100%, make sure the default behavior doesn't block rollout
	defLimit                    = intstr.FromString("100%")
	defAutoPartitionSize        = intstr.FromString("25%")
	defMaxUnavailablePartitions = intstr.FromInt(0)
)

const (
	maxTemplateRecursionDepth = 50
	clusterLabelPrefix        = "global.fleet.clusterLabels."
	byBundleIndexerName       = "fleet.byBundle"
)

// BundleFromDeployment returns the namespace and name of the bundle that
// created the bundledeployment
func BundleFromDeployment(labels map[string]string) (string, string) {
	return labels[fleet.BundleNamespaceLabel],
		labels[fleet.BundleLabel]
}

// Target represents a bundle deployment target, encapsulating all relevant
// information about the deployment, associated cluster groups, cluster,
// bundle, deployment options, and deployment identifier.
type Target struct {
	Deployment    *fleet.BundleDeployment
	ClusterGroups []*fleet.ClusterGroup
	Cluster       *fleet.Cluster
	Bundle        *fleet.Bundle
	Options       fleet.BundleDeploymentOptions
	DeploymentID  string
}

// BundleDeployment returns a new BundleDeployment, it discards annotations, status, etc.
// The labels are copied from the Bundle.
func (t *Target) BundleDeployment() *fleet.BundleDeployment {
	bd := &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.Deployment.Name,
			Namespace: t.Deployment.Namespace,
			Labels:    t.BundleDeploymentLabels(t.Cluster.Namespace, t.Cluster.Name),
		},
		Spec: t.Deployment.Spec,
	}
	bd.Spec.Paused = t.IsPaused()

	initialiseOptionsMaps(bd)

	for _, bundleTarget := range t.Bundle.Spec.Targets {
		for key, value := range bundleTarget.NamespaceLabels {
			bd.Spec.Options.NamespaceLabels[key] = value
			bd.Spec.StagedOptions.NamespaceLabels[key] = value
		}

		for key, value := range bundleTarget.NamespaceAnnotations {
			bd.Spec.Options.NamespaceAnnotations[key] = value
			bd.Spec.StagedOptions.NamespaceAnnotations[key] = value
		}
	}

	bd.Spec.DependsOn = t.Bundle.Spec.DependsOn
	bd.Spec.CorrectDrift = t.Options.CorrectDrift
	return bd
}

func (t *Target) IsPaused() bool {
	return t.Cluster.Spec.Paused ||
		t.Bundle.Spec.Paused
}

// BundleDeploymentLabels builds all labels for a bundledeployment
func (t *Target) BundleDeploymentLabels(clusterNamespace string, clusterName string) map[string]string {
	// remove labels starting with kubectl.kubernetes.io or containing
	// cattle.io from bundle
	labels := yaml.CleanAnnotationsForExport(t.Bundle.Labels)

	// copy fleet labels from bundle to bundledeployment
	for k, v := range t.Bundle.Labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") {
			labels[k] = v
		}
	}

	// labels for the bundledeployment by bundle selector
	labels[fleet.BundleLabel] = t.Bundle.Name
	labels[fleet.BundleNamespaceLabel] = t.Bundle.Namespace

	// ManagedLabel allows clean up of the bundledeployment
	labels[fleet.ManagedLabel] = "true"

	// add labels to identify the cluster this bundledeployment belongs to
	labels[fleet.ClusterNamespaceLabel] = clusterNamespace
	labels[fleet.ClusterLabel] = clusterName

	return labels
}

func (t *Target) modified() []fleet.ModifiedStatus {
	if t.Deployment == nil {
		return nil
	}
	return t.Deployment.Status.ModifiedStatus
}

func (t *Target) nonReady() []fleet.NonReadyStatus {
	if t.Deployment == nil {
		return nil
	}
	return t.Deployment.Status.NonReadyStatus
}

// state calculates a fleet.BundleState from t (pure function)
func (t *Target) state() fleet.BundleState {
	switch t.Deployment {
	case nil:
		return fleet.Pending
	default:
		return summary.GetDeploymentState(t.Deployment)
	}
}

// message returns a relevant message from the target (pure function)
func (t *Target) message() string {
	return summary.MessageFromDeployment(t.Deployment)
}

// initialiseOptionsMaps initialises options and staged options maps of namespace labels and annotations on bd, if
// needed.
// Assumes that bd is not nil.
func initialiseOptionsMaps(bd *fleet.BundleDeployment) {
	if bd.Spec.Options.NamespaceLabels == nil {
		bd.Spec.Options.NamespaceLabels = map[string]string{}
	}

	if bd.Spec.Options.NamespaceAnnotations == nil {
		bd.Spec.Options.NamespaceAnnotations = map[string]string{}
	}

	if bd.Spec.StagedOptions.NamespaceLabels == nil {
		bd.Spec.StagedOptions.NamespaceLabels = map[string]string{}
	}

	if bd.Spec.StagedOptions.NamespaceAnnotations == nil {
		bd.Spec.StagedOptions.NamespaceAnnotations = map[string]string{}
	}
}
