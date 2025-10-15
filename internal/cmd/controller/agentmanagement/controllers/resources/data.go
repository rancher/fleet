package resources

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/apply"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	BundleDeploymentClusterRole = "fleet-bundle-deployment"
	ContentClusterRole          = "fleet-content"
)

// ApplyBootstrapResources creates the cluster roles, system namespace and system registration namespace
func ApplyBootstrapResources(systemNamespace, systemRegistrationNamespace string, apply apply.Apply) error {
	return apply.ApplyObjects(
		// used by request-* service accounts from agents
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: BundleDeploymentClusterRole,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "list", "watch"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.BundleDeploymentResourceNamePlural},
				},
				{
					Verbs:     []string{"update", "patch"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.BundleDeploymentResourceNamePlural + "/status"},
				},
				{
					// Needed for copying a bundle's `DownstreamResources`, of which config
					// maps are a supported kind, from the agent.
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
				},
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"secrets"},
				},
			},
		},
		// used by request-* service accounts from agents
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: ContentClusterRole,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.ContentResourceNamePlural},
				},
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: systemNamespace,
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: systemRegistrationNamespace,
			},
		},
	)
}
