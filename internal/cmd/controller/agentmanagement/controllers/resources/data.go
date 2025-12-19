package resources

import (
	"context"

	"github.com/rancher/fleet/internal/experimental"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	BundleDeploymentClusterRole = "fleet-bundle-deployment"
	ContentClusterRole          = "fleet-content"
)

// ApplyBootstrapResources creates the cluster roles, system namespace and system registration namespace
func ApplyBootstrapResources(ctx context.Context, c client.Client, systemNamespace, systemRegistrationNamespace string) error {
	rules := []rbacv1.PolicyRule{
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
			Verbs:     []string{"get"},
			APIGroups: []string{""},
			Resources: []string{"secrets"},
		},
	}

	if experimental.CopyResourcesDownstreamEnabled() {
		rules = append(rules, rbacv1.PolicyRule{
			Verbs:     []string{"get"},
			APIGroups: []string{""},
			Resources: []string{"configmaps"},
		})
	}

	objs := []client.Object{
		// used by request-* service accounts from agents
		&rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: rbacv1.SchemeGroupVersion.String(),
				Kind:       "ClusterRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: BundleDeploymentClusterRole,
			},
			Rules: rules,
		},
		// used by request-* service accounts from agents
		&rbacv1.ClusterRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: rbacv1.SchemeGroupVersion.String(),
				Kind:       "ClusterRole",
			},
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
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: systemNamespace,
			},
		},
		&corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "Namespace",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: systemRegistrationNamespace,
			},
		},
	}

	// Apply each object using Server-Side Apply
	for _, obj := range objs {
		if err := c.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner("fleet-agentmanagement-bootstrap")); err != nil {
			return err
		}
	}

	return nil
}
