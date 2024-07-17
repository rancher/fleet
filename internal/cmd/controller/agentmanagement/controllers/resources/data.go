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
	ImportRegistration          = "fleet-import-registration"
	ImportCredentials           = "fleet-import-creds" // nolint:gosec // this is not a credential
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
					Verbs:     []string{"update"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.BundleDeploymentResourceNamePlural + "/status"},
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
		// used by import- service accounts
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: ImportCredentials,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"secrets"},
				},
			},
		},
		// used by import- service accounts
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: ImportRegistration,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.ClusterRegistrationResourceNamePlural},
				},
			},
		},
		// namespace for the controllers (e.g. cattle-fleet-system)
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: systemNamespace,
			},
		},
		// namespace for secrets used in the cluster registration process (e.g. cattle-fleet-clusters-system)
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: systemRegistrationNamespace,
			},
		},
	)
}
