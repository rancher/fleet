package v1alpha1

import (
	"fmt"
	"strings"

	"github.com/rancher/wrangler/pkg/genericcondition"
	"github.com/rancher/wrangler/pkg/summary"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	// Ready: Bundles have been deployed and all resources are ready.
	Ready BundleState = "Ready"
	// NotReady: Bundles have been deployed and some resources are not
	// ready.
	NotReady BundleState = "NotReady"
	// WaitApplied: Bundles have been synced from Fleet controller and
	// downstream cluster, but are waiting to be deployed.
	WaitApplied BundleState = "WaitApplied"
	// ErrApplied: Bundles have been synced from the Fleet controller and
	// the downstream cluster, but there were some errors when deploying
	// the Bundle.
	ErrApplied BundleState = "ErrApplied"
	// OutOfSync: Bundles have been synced from Fleet controller, but
	// downstream agent hasn't synced the change yet.
	OutOfSync BundleState = "OutOfSync"
	// Pending: Bundles are being processed by Fleet controller.
	Pending BundleState = "Pending"
	// Modified: Bundles have been deployed and all resources are ready,
	// but there are some changes that were not made from the Git
	// Repository.
	Modified BundleState = "Modified"

	// StateRank ranks the state, e.g. so the highest ranked non-ready
	// state can be reported in a summary.
	StateRank = map[BundleState]int{
		ErrApplied:  7,
		WaitApplied: 6,
		Modified:    5,
		OutOfSync:   4,
		Pending:     3,
		NotReady:    2,
		Ready:       1,
	}
)

// MaxHelmReleaseNameLen is the maximum length of a Helm release name.
// See https://github.com/helm/helm/blob/293b50c65d4d56187cd4e2f390f0ada46b4c4737/pkg/chartutil/validate_name.go#L54-L61
const MaxHelmReleaseNameLen = 53

type BundleState string

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Bundle contains the resources of an application and its deployment options.
// It will be deployed as a Helm chart to target clusters.
//
// When a GitRepo is scanned it will produce one or more bundles. Bundles are
// a collection of resources that get deployed to one or more cluster(s). Bundle is the
// fundamental deployment unit used in Fleet. The contents of a Bundle may be
// Kubernetes manifests, Kustomize configuration, or Helm charts. Regardless
// of the source the contents are dynamically rendered into a Helm chart by
// the agent and installed into the downstream cluster as a Helm release.
type Bundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleSpec   `json:"spec"`
	Status BundleStatus `json:"status"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BundleNamespaceMapping maps bundles to clusters in other namespaces.
type BundleNamespaceMapping struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	BundleSelector    *metav1.LabelSelector `json:"bundleSelector,omitempty"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}

type BundleSpec struct {
	BundleDeploymentOptions

	// Paused if set to true, will stop any BundleDeployments from being updated. It will be marked as out of sync.
	Paused bool `json:"paused,omitempty"`

	// RolloutStrategy controls the rollout of bundles, by defining
	// partitions, canaries and percentages for cluster availability.
	RolloutStrategy *RolloutStrategy `json:"rolloutStrategy,omitempty"`

	// Resources contains the resources that were read from the bundle's
	// path. This includes the content of downloaded helm charts.
	Resources []BundleResource `json:"resources,omitempty"`

	// Targets refer to the clusters which will be deployed to.
	// Targets are evaluated in order and the first one to match is used.
	Targets []BundleTarget `json:"targets,omitempty"`

	// TargetRestrictions is an allow list, which controls if a bundledeployment is created for a target.
	TargetRestrictions []BundleTargetRestriction `json:"targetRestrictions,omitempty"`

	// DependsOn refers to the bundles which must be ready before this bundle can be deployed.
	DependsOn []BundleRef `json:"dependsOn,omitempty"`
}

type BundleRef struct {
	// Name of the bundle.
	Name string `json:"name,omitempty"`
	// Selector matching bundle's labels.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// BundleResource represents the content of a single resource from the bundle, like a YAML manifest.
type BundleResource struct {
	// Name of the resource, can include the bundle's internal path.
	Name string `json:"name,omitempty"`
	// The content of the resource, can be compressed.
	Content string `json:"content,omitempty"`
	// Encoding is either empty or "base64+gz".
	Encoding string `json:"encoding,omitempty"`
}

// RolloverStrategy controls the rollout of the bundle across clusters.
type RolloutStrategy struct {
	// A number or percentage of clusters that can be unavailable during an update
	// of a bundle. This follows the same basic approach as a deployment rollout
	// strategy. Once the number of clusters meets unavailable state update will be
	// paused. Default value is 100% which doesn't take effect on update.
	// default: 100%
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// A number or percentage of cluster partitions that can be unavailable during
	// an update of a bundle.
	// default: 0
	MaxUnavailablePartitions *intstr.IntOrString `json:"maxUnavailablePartitions,omitempty"`
	// A number or percentage of how to automatically partition clusters if no
	// specific partitioning strategy is configured.
	// default: 25%
	AutoPartitionSize *intstr.IntOrString `json:"autoPartitionSize,omitempty"`
	// A list of definitions of partitions.  If any target clusters do not match
	// the configuration they are added to partitions at the end following the
	// autoPartitionSize.
	Partitions []Partition `json:"partitions,omitempty"`
}

// Partition defines a separate rollout strategy for a set of clusters.
type Partition struct {
	// A user-friendly name given to the partition used for Display (optional).
	Name string `json:"name,omitempty"`
	// A number or percentage of clusters that can be unavailable in this
	// partition before this partition is treated as done.
	// default: 10%
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// ClusterName is the name of a cluster to include in this partition
	ClusterName string `json:"clusterName,omitempty"`
	// Selector matching cluster labels to include in this partition
	ClusterSelector *metav1.LabelSelector `json:"clusterSelector,omitempty"`
	// A cluster group name to include in this partition
	ClusterGroup string `json:"clusterGroup,omitempty"`
	// Selector matching cluster group labels to include in this partition
	ClusterGroupSelector *metav1.LabelSelector `json:"clusterGroupSelector,omitempty"`
}

// BundleTargetRestriction is used internally by Fleet and should not be modified.
// It acts as an allow list, to prevent the creation of BundleDeployments from
// Targets created by TargetCustomizations in fleet.yaml.
type BundleTargetRestriction struct {
	Name                 string                `json:"name,omitempty"`
	ClusterName          string                `json:"clusterName,omitempty"`
	ClusterSelector      *metav1.LabelSelector `json:"clusterSelector,omitempty"`
	ClusterGroup         string                `json:"clusterGroup,omitempty"`
	ClusterGroupSelector *metav1.LabelSelector `json:"clusterGroupSelector,omitempty"`
}

// BundleTarget declares clusters to deploy to. Fleet will merge the
// BundleDeploymentOptions from customizations into this struct.
type BundleTarget struct {
	BundleDeploymentOptions
	// Name of target. This value is largely for display and logging. If
	// not specified a default name of the format "target000" will be used
	Name string `json:"name,omitempty"`
	// ClusterName to match a specific cluster by name that will be
	// selected
	ClusterName string `json:"clusterName,omitempty"`
	// ClusterSelector is a selector to match clusters. The structure is
	// the standard metav1.LabelSelector format. If clusterGroupSelector or
	// clusterGroup is specified, clusterSelector will be used only to
	// further refine the selection after clusterGroupSelector and
	// clusterGroup is evaluated.
	ClusterSelector *metav1.LabelSelector `json:"clusterSelector,omitempty"`
	// ClusterGroup to match a specific cluster group by name.
	ClusterGroup string `json:"clusterGroup,omitempty"`
	// ClusterGroupSelector is a selector to match cluster groups.
	ClusterGroupSelector *metav1.LabelSelector `json:"clusterGroupSelector,omitempty"`
	// DoNotDeploy if set to true, will not deploy to this target.
	DoNotDeploy bool `json:"doNotDeploy,omitempty"`
}

// BundleSummary contains the number of bundle deployments in each state and a
// list of non-ready resources. It is used in the bundle, clustergroup, cluster
// and gitrepo status.
type BundleSummary struct {
	// NotReady is the number of bundle deployments that have been deployed
	// where some resources are not ready.
	NotReady int `json:"notReady,omitempty"`
	// WaitApplied is the number of bundle deployments that have been
	// synced from Fleet controller and downstream cluster, but are waiting
	// to be deployed.
	WaitApplied int `json:"waitApplied,omitempty"`
	// ErrApplied is the number of bundle deployments that have been synced
	// from the Fleet controller and the downstream cluster, but with some
	// errors when deploying the bundle.
	ErrApplied int `json:"errApplied,omitempty"`
	// OutOfSync is the number of bundle deployments that have been synced
	// from Fleet controller, but not yet by the downstream agent.
	OutOfSync int `json:"outOfSync,omitempty"`
	// Modified is the number of bundle deployments that have been deployed
	// and for which all resources are ready, but where some changes from the
	// Git repository have not yet been synced.
	Modified int `json:"modified,omitempty"`
	// Ready is the number of bundle deployments that have been deployed
	// where all resources are ready.
	Ready int `json:"ready"`
	// Pending is the number of bundle deployments that are being processed
	// by Fleet controller.
	Pending int `json:"pending,omitempty"`
	// DesiredReady is the number of bundle deployments that should be
	// ready.
	DesiredReady int `json:"desiredReady"`
	// NonReadyClusters is a list of states, which is filled for a bundle
	// that is not ready.
	NonReadyResources []NonReadyResource `json:"nonReadyResources,omitempty"`
}

// NonReadyResource contains information about a bundle that is not ready for a
// given state like "ErrApplied". It contains a list of non-ready or modified
// resources and their states.
type NonReadyResource struct {
	// Name is the name of the resource.
	Name string `json:"name,omitempty"`
	// State is the state of the resource, like e.g. "NotReady" or "ErrApplied".
	State BundleState `json:"bundleState,omitempty"`
	// Message contains information why the bundle is not ready.
	Message string `json:"message,omitempty"`
	// ModifiedStatus lists the state for each modified resource.
	ModifiedStatus []ModifiedStatus `json:"modifiedStatus,omitempty"`
	// NonReadyStatus lists the state for each non-ready resource.
	NonReadyStatus []NonReadyStatus `json:"nonReadyStatus,omitempty"`
}

var (
	// BundleConditionReady is unused. A "Ready" condition on a bundle
	// indicates that its resources are ready and the dependencies are
	// fulfilled.
	BundleConditionReady = "Ready"
	// BundleDeploymentConditionReady is the condition that displays for
	// status in general and it is used for the readiness of resources.
	BundleDeploymentConditionReady = "Ready"
	// BundleDeploymentConditionInstalled indicates the bundledeployment
	// has been installed.
	BundleDeploymentConditionInstalled = "Installed"
	// BundleDeploymentConditionDeployed is used by the bundledeployment
	// controller. It is true if the handler returns no error and false if
	// an error is returned.
	BundleDeploymentConditionDeployed = "Deployed"
)

type BundleStatus struct {
	// Conditions is a list of Wrangler conditions that describe the state
	// of the bundle.
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`

	// Summary contains the number of bundle deployments in each state and
	// a list of non-ready resources.
	Summary BundleSummary `json:"summary,omitempty"`
	// NewlyCreated is the number of bundle deployments that have been created,
	// not updated.
	NewlyCreated int `json:"newlyCreated,omitempty"`
	// Unavailable is the number of bundle deployments that are not ready or
	// where the AppliedDeploymentID in the status does not match the
	// DeploymentID from the spec.
	Unavailable int `json:"unavailable"`
	// UnavailablePartitions is the number of unavailable partitions.
	UnavailablePartitions int `json:"unavailablePartitions"`
	// MaxUnavailable is the maximum number of unavailable deployments. See
	// rollout configuration.
	MaxUnavailable int `json:"maxUnavailable"`
	// MaxUnavailablePartitions is the maximum number of unavailable
	// partitions. The rollout configuration defines a maximum number or
	// percentage of unavailable partitions.
	MaxUnavailablePartitions int `json:"maxUnavailablePartitions"`
	// MaxNew is always 50. A bundle change can only stage 50
	// bundledeployments at a time.
	MaxNew int `json:"maxNew,omitempty"`
	// PartitionStatus lists the status of each partition.
	PartitionStatus []PartitionStatus `json:"partitions,omitempty"`
	// Display contains the number of ready, desiredready clusters and a
	// summary state for the bundle's resources.
	Display BundleDisplay `json:"display,omitempty"`
	// ResourceKey lists resources, which will likely be deployed. The
	// actual list of resources on a cluster might differ, depending on the
	// helm chart, value templating, etc..
	ResourceKey []ResourceKey `json:"resourceKey,omitempty"`
	// ObservedGeneration is the current generation of the bundle.
	ObservedGeneration int64 `json:"observedGeneration"`
}

// ResourceKey lists resources, which will likely be deployed.
type ResourceKey struct {
	// Kind is the k8s api kind of the resource.
	Kind string `json:"kind,omitempty"`
	// APIVersion is the k8s api version of the resource.
	APIVersion string `json:"apiVersion,omitempty"`
	// Namespace is the namespace of the resource.
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the resource.
	Name string `json:"name,omitempty"`
}

// BundleDisplay contains the number of ready, desiredready clusters and a
// summary state for the bundle.
type BundleDisplay struct {
	// ReadyClusters is a string in the form "%d/%d", that describes the
	// number of clusters that are ready vs. the number of clusters desired
	// to be ready.
	ReadyClusters string `json:"readyClusters,omitempty"`
	// State is a summary state for the bundle, calculated over the non-ready resources.
	State string `json:"state,omitempty"`
}

// PartitionStatus is the status of a single rollout partition.
type PartitionStatus struct {
	// Name is the name of the partition.
	Name string `json:"name,omitempty"`
	// Count is the number of clusters in the partition.
	Count int `json:"count,omitempty"`
	// MaxUnavailable is the maximum number of unavailable clusters in the partition.
	MaxUnavailable int `json:"maxUnavailable,omitempty"`
	// Unavailable is the number of unavailable clusters in the partition.
	Unavailable int `json:"unavailable,omitempty"`
	// Summary is a summary state for the partition, calculated over its non-ready resources.
	Summary BundleSummary `json:"summary,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BundleDeployment is used internally by Fleet and should not be used directly.
// When a Bundle is deployed to a cluster an instance of a Bundle is called a
// BundleDeployment. A BundleDeployment represents the state of that Bundle on
// a specific cluster with its cluster-specific customizations. The Fleet agent
// is only aware of BundleDeployment resources that are created for the cluster
// the agent is managing.
type BundleDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleDeploymentSpec   `json:"spec,omitempty"`
	Status BundleDeploymentStatus `json:"status,omitempty"`
}

type BundleDeploymentOptions struct {
	// DefaultNamespace is the namespace to use for resources that do not
	// specify a namespace. This field is not used to enforce or lock down
	// the deployment to a specific namespace.
	DefaultNamespace string `json:"defaultNamespace,omitempty"`

	// TargetNamespace if present will assign all resource to this
	// namespace and if any cluster scoped resource exists the deployment
	// will fail.
	TargetNamespace string `json:"namespace,omitempty"`

	// Kustomize options for the deployment, like the dir containing the
	// kustomization.yaml file.
	Kustomize *KustomizeOptions `json:"kustomize,omitempty"`

	// Helm options for the deployment, like the chart name, repo and values.
	Helm *HelmOptions `json:"helm,omitempty"`

	// ServiceAccount which will be used to perform this deployment.
	ServiceAccount string `json:"serviceAccount,omitempty"`

	// ForceSyncGeneration is used to force a redeployment
	ForceSyncGeneration int64 `json:"forceSyncGeneration,omitempty"`

	// YAML options, if using raw YAML these are names that map to
	// overlays/{name} files that will be used to replace or patch a resource.
	YAML *YAMLOptions `json:"yaml,omitempty"`

	// Diff can be used to ignore the modified state of objects which are amended at runtime.
	Diff *DiffOptions `json:"diff,omitempty"`

	// KeepResources can be used to keep the deployed resources when removing the bundle
	KeepResources bool `json:"keepResources,omitempty"`

	//IgnoreOptions can be used to ignore fields when monitoring the bundle.
	IgnoreOptions `json:"ignore,omitempty"`

	// CorrectDrift specifies how drift correction should work.
	CorrectDrift CorrectDrift `json:"correctDrift,omitempty"`

	// NamespaceLabels are labels that will be appended to the namespace created by Fleet.
	NamespaceLabels *map[string]string `json:"namespaceLabels,omitempty"`

	// NamespaceAnnotations are annotations that will be appended to the namespace created by Fleet.
	NamespaceAnnotations *map[string]string `json:"namespaceAnnotations,omitempty"`
}

type DiffOptions struct {
	// ComparePatches match a resource and remove fields from the check for modifications.
	ComparePatches []ComparePatch `json:"comparePatches,omitempty"`
}

// ComparePatch matches a resource and removes fields from the check for modifications.
type ComparePatch struct {
	// Kind is the kind of the resource to match.
	Kind string `json:"kind,omitempty"`
	// APIVersion is the apiVersion of the resource to match.
	APIVersion string `json:"apiVersion,omitempty"`
	// Namespace is the namespace of the resource to match.
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the resource to match.
	Name string `json:"name,omitempty"`
	// Operations remove a JSON path from the resource.
	Operations []Operation `json:"operations,omitempty"`
	// JSONPointers ignore diffs at a certain JSON path.
	JsonPointers []string `json:"jsonPointers,omitempty"`
}

// Operation of a ComparePatch, usually "remove".
type Operation struct {
	// Op is usually "remove"
	Op string `json:"op,omitempty"`
	// Path is the JSON path to remove.
	Path string `json:"path,omitempty"`
	// Value is usually empty.
	Value string `json:"value,omitempty"`
}

// YAMLOptions, if using raw YAML these are names that map to
// overlays/{name} files that will be used to replace or patch a resource.
type YAMLOptions struct {
	// Overlays is a list of names that maps to folders in "overlays/".
	// If you wish to customize the file ./subdir/resource.yaml then a file
	// ./overlays/myoverlay/subdir/resource.yaml will replace the base
	// file.
	// A file named ./overlays/myoverlay/subdir/resource_patch.yaml will patch the base file.
	Overlays []string `json:"overlays,omitempty"`
}

// KustomizeOptions for a deployment.
type KustomizeOptions struct {
	// Dir points to a custom folder for kustomize resources. This folder must contain
	// a kustomization.yaml file.
	Dir string `json:"dir,omitempty"`
}

// HelmOptions for the deployment. For Helm-based bundles, all options can be
// used, otherwise some options are ignored. For example ReleaseName works with
// all bundle types.
type HelmOptions struct {
	// Chart can refer to any go-getter URL or OCI registry based helm
	// chart URL. The chart will be downloaded.
	Chart string `json:"chart,omitempty"`

	// Repo is the name of the HTTPS helm repo to download the chart from.
	Repo string `json:"repo,omitempty"`

	// ReleaseName sets a custom release name to deploy the chart as. If
	// not specified a release name will be generated by combining the
	// invoking GitRepo.name + GitRepo.path.
	ReleaseName string `json:"releaseName,omitempty"`

	// Version of the chart to download
	Version string `json:"version,omitempty"`

	// TimeoutSeconds is the time to wait for Helm operations.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// Values passed to Helm. It is possible to specify the keys and values
	// as go template strings.
	Values *GenericMap `json:"values,omitempty"`

	// ValuesFrom loads the values from configmaps and secrets.
	ValuesFrom []ValuesFrom `json:"valuesFrom,omitempty"`

	// Force allows to override immutable resources. This could be dangerous.
	Force bool `json:"force,omitempty"`

	// TakeOwnership makes helm skip the check for its own annotations
	TakeOwnership bool `json:"takeOwnership,omitempty"`

	// MaxHistory limits the maximum number of revisions saved per release by Helm.
	MaxHistory int `json:"maxHistory,omitempty"`

	// ValuesFiles is a list of files to load values from.
	ValuesFiles []string `json:"valuesFiles,omitempty"`

	// WaitForJobs if set and timeoutSeconds provided, will wait until all
	// Jobs have been completed before marking the GitRepo as ready. It
	// will wait for as long as timeoutSeconds
	WaitForJobs bool `json:"waitForJobs,omitempty"`

	// Atomic sets the --atomic flag when Helm is performing an upgrade
	Atomic bool `json:"atomic,omitempty"`

	// DisablePreProcess disables template processing in values
	DisablePreProcess bool `json:"disablePreProcess,omitempty"`

	// DisableDNS can be used to customize Helm's EnableDNS option, which Fleet sets to `true` by default.
	DisableDNS bool `json:"disableDNS,omitempty"`
}

// IgnoreOptions defines conditions to be ignored when monitoring the Bundle.
type IgnoreOptions struct {
	// Conditions is a list of conditions to be ignored when monitoring the Bundle.
	Conditions []map[string]string `json:"conditions,omitempty"`
}

// Define helm values that can come from configmap, secret or external. Credit: https://github.com/fluxcd/helm-operator/blob/0cfea875b5d44bea995abe7324819432070dfbdc/pkg/apis/helm.fluxcd.io/v1/types_helmrelease.go#L439
type ValuesFrom struct {
	// The reference to a config map with release values.
	// +optional
	ConfigMapKeyRef *ConfigMapKeySelector `json:"configMapKeyRef,omitempty"`
	// The reference to a secret with release values.
	// +optional
	SecretKeyRef *SecretKeySelector `json:"secretKeyRef,omitempty"`
}

type ConfigMapKeySelector struct {
	LocalObjectReference `json:",inline"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +optional
	Key string `json:"key,omitempty"`
}

type SecretKeySelector struct {
	LocalObjectReference `json:",inline"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +optional
	Key string `json:"key,omitempty"`
}

type LocalObjectReference struct {
	// Name of a resource in the same namespace as the referent.
	Name string `json:"name"`
}

type BundleDeploymentSpec struct {
	// Paused if set to true, will stop any BundleDeployments from being
	// updated. If true, BundleDeployments will be marked as out of sync
	// when changes are detected.
	Paused bool `json:"paused,omitempty"`
	// StagedOptions are the deployment options, that are staged for
	// the next deployment.
	StagedOptions BundleDeploymentOptions `json:"stagedOptions,omitempty"`
	// StagedDeploymentID is the ID of the staged deployment.
	StagedDeploymentID string `json:"stagedDeploymentID,omitempty"`
	// Options are the deployment options, that are currently applied.
	Options BundleDeploymentOptions `json:"options,omitempty"`
	// DeploymentID is the ID of the currently applied deployment.
	DeploymentID string `json:"deploymentID,omitempty"`
	// DependsOn refers to the bundles which must be ready before this bundle can be deployed.
	DependsOn []BundleRef `json:"dependsOn,omitempty"`
	// CorrectDrift specifies how drift correction should work.
	CorrectDrift CorrectDrift `json:"correctDrift,omitempty"`
}

// BundleDeploymentResource contains the metadata of a deployed resource.
type BundleDeploymentResource struct {
	Kind       string      `json:"kind,omitempty"`
	APIVersion string      `json:"apiVersion,omitempty"`
	Namespace  string      `json:"namespace,omitempty"`
	Name       string      `json:"name,omitempty"`
	CreatedAt  metav1.Time `json:"createdAt,omitempty"`
}

type BundleDeploymentStatus struct {
	Conditions          []genericcondition.GenericCondition `json:"conditions,omitempty"`
	AppliedDeploymentID string                              `json:"appliedDeploymentID,omitempty"`
	Release             string                              `json:"release,omitempty"`
	Ready               bool                                `json:"ready,omitempty"`
	NonModified         bool                                `json:"nonModified,omitempty"`
	NonReadyStatus      []NonReadyStatus                    `json:"nonReadyStatus,omitempty"`
	ModifiedStatus      []ModifiedStatus                    `json:"modifiedStatus,omitempty"`
	Display             BundleDeploymentDisplay             `json:"display,omitempty"`
	SyncGeneration      *int64                              `json:"syncGeneration,omitempty"`
	// Resources lists the metadata of resources that were deployed
	// according to the helm release history.
	Resources []BundleDeploymentResource `json:"resources,omitempty"`
}

type BundleDeploymentDisplay struct {
	Deployed  string `json:"deployed,omitempty"`
	Monitored string `json:"monitored,omitempty"`
	State     string `json:"state,omitempty"`
}

// NonReadyStatus is used to report the status of a resource that is not ready. It includes a summary.
type NonReadyStatus struct {
	UID        types.UID       `json:"uid,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	APIVersion string          `json:"apiVersion,omitempty"`
	Namespace  string          `json:"namespace,omitempty"`
	Name       string          `json:"name,omitempty"`
	Summary    summary.Summary `json:"summary,omitempty"`
}

func (in NonReadyStatus) String() string {
	return name(in.APIVersion, in.Kind, in.Namespace, in.Name) + " " + in.Summary.String()
}

func name(apiVersion, kind, namespace, name string) string {
	if apiVersion == "" {
		if namespace == "" {
			return fmt.Sprintf("%s %s", strings.ToLower(kind), name)
		}
		return fmt.Sprintf("%s %s/%s", strings.ToLower(kind), namespace, name)
	}
	if namespace == "" {
		return fmt.Sprintf("%s.%s %s", strings.ToLower(kind), strings.SplitN(apiVersion, "/", 2)[0], name)
	}
	return fmt.Sprintf("%s.%s %s/%s", strings.ToLower(kind), strings.SplitN(apiVersion, "/", 2)[0], namespace, name)
}

// ModifiedStatus is used to report the status of a resource that is modified.
// It indicates if the modification was a create, a delete or a patch.
type ModifiedStatus struct {
	Kind       string `json:"kind,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
	Create     bool   `json:"missing,omitempty"`
	Delete     bool   `json:"delete,omitempty"`
	Patch      string `json:"patch,omitempty"`
}

func (in ModifiedStatus) String() string {
	msg := name(in.APIVersion, in.Kind, in.Namespace, in.Name)
	if in.Create {
		return msg + " missing"
	} else if in.Delete {
		return msg + " extra"
	}
	return msg + " modified " + in.Patch
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Content is used internally by Fleet and should not be used directly. It
// contains the resources from a bundle for a specific target cluster.
type Content struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Content is a byte array, which contains the manifests of a bundle.
	// The bundle resources are copied into the bundledeployment's content
	// resource, so the downstream agent can deploy them.
	Content []byte `json:"content,omitempty"`
}
