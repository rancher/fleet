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
	Ready      BundleState = "Ready"
	NotReady   BundleState = "NotReady"
	NotApplied BundleState = "NotApplied"
	OutOfSync  BundleState = "OutOfSync"
	Pending    BundleState = "Pending"
	Modified   BundleState = "Modified"
)

type BundleState string

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Bundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleSpec   `json:"spec"`
	Status BundleStatus `json:"status"`
}

type BundleSpec struct {
	BundleDeploymentOptions

	Paused          bool             `json:"paused,omitempty"`
	RolloutStrategy *RolloutStrategy `json:"rolloutStrategy,omitempty"`
	Resources       []BundleResource `json:"resources,omitempty"`
	Overlays        []BundleOverlay  `json:"overlays,omitempty"`
	Targets         []BundleTarget   `json:"targets,omitempty"`
}

type BundleResource struct {
	Name     string `json:"name,omitempty"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

type RolloutStrategy struct {
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

type BundleOverlay struct {
	BundleDeploymentOptions

	Name      string           `json:"name,omitempty"`
	Overlays  []string         `json:"overlays,omitempty"`
	Resources []BundleResource `json:"resources,omitempty"`
}

type BundleTarget struct {
	BundleDeploymentOptions
	Name                 string                `json:"name,omitempty"`
	ClusterSelector      *metav1.LabelSelector `json:"clusterSelector,omitempty"`
	ClusterGroup         string                `json:"clusterGroup,omitempty"`
	ClusterGroupSelector *metav1.LabelSelector `json:"clusterGroupSelector,omitempty"`
	Overlays             []string              `json:"overlays,omitempty"`
}

type BundleSummary struct {
	NotReady          int                `json:"notReady,omitempty"`
	NotApplied        int                `json:"notApplied,omitempty"`
	OutOfSync         int                `json:"outOfSync,omitempty"`
	Modified          int                `json:"modified,omitempty"`
	Ready             int                `json:"ready"`
	Pending           int                `json:"pending,omitempty"`
	DesiredReady      int                `json:"desiredReady"`
	NonReadyResources []NonReadyResource `json:"nonReadyResources,omitempty"`
}

type NonReadyResource struct {
	Name    string      `json:"name,omitempty"`
	State   BundleState `json:"bundleState,omitempty"`
	Message string      `json:"message,omitempty"`
}

var (
	BundleConditionReady           = "Ready"
	BundleDeploymentConditionReady = "Ready"
)

type BundleStatus struct {
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`

	Summary        BundleSummary `json:"summary,omitempty"`
	NewlyCreated   int           `json:"newlyCreated,omitempty"`
	Unavailable    int           `json:"unavailable,omitempty"`
	MaxUnavailable int           `json:"maxUnavailable,omitempty"`
	MaxNew         int           `json:"maxNew,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BundleDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleDeploymentSpec   `json:"spec,omitempty"`
	Status BundleDeploymentStatus `json:"status,omitempty"`
}

type BundleDeploymentOptions struct {
	DefaultNamespace string      `json:"defaultNamespace,omitempty"`
	KustomizeDir     string      `json:"kustomizeDir,omitempty"`
	TimeoutSeconds   int         `json:"timeoutSeconds,omitempty"`
	Values           *GenericMap `json:"values,omitempty"`
}

type BundleDeploymentSpec struct {
	StagedOptions      BundleDeploymentOptions `json:"stagedOptions,omitempty"`
	StagedDeploymentID string                  `json:"stagedDeploymentID,omitempty"`
	Options            BundleDeploymentOptions `json:"options,omitempty"`
	DeploymentID       string                  `json:"deploymentID,omitempty"`
}

type BundleDeploymentStatus struct {
	Conditions          []genericcondition.GenericCondition `json:"conditions,omitempty"`
	AppliedDeploymentID string                              `json:"appliedDeploymentID,omitempty"`
	Release             string                              `json:"release,omitempty"`
	Ready               bool                                `json:"ready,omitempty"`
	NonModified         bool                                `json:"nonModified,omitempty"`
	NonReadyStatus      []NonReadyStatus                    `json:"nonReadyStatus,omitempty"`
	ModifiedStatus      []ModifiedStatus                    `json:"modifiedStatus,omitempty"`
}

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
	return msg + " modified"
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Content struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Content []byte `json:"content,omitempty"`
}
