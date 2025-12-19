package clusterregistrationtoken

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	yaml "sigs.k8s.io/yaml"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// ClusterRegistrationTokenReconciler reconciles a ClusterRegistrationToken object
type ClusterRegistrationTokenReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	SystemNamespace             string
	SystemRegistrationNamespace string
}

// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusterregistrationtokens,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusterregistrationtokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles ClusterRegistrationToken reconciliation
func (r *ClusterRegistrationTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("clusterregistrationtoken", req.NamespacedName)

	// Fetch the ClusterRegistrationToken
	token := &fleet.ClusterRegistrationToken{}
	if err := r.Get(ctx, req.NamespacedName, token); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("ClusterRegistrationToken not found, ignoring")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check if expired and delete if needed
	if gone, result, err := r.deleteExpired(ctx, token); gone || err != nil {
		return result, err
	}

	logger.Info("Reconciling ClusterRegistrationToken", "namespace", token.Namespace, "name", token.Name)

	// Generate service account name
	saName := names.SafeConcatName(token.Name, string(token.UID))

	// Get or create service account
	sa := &corev1.ServiceAccount{}
	err := r.Get(ctx, types.NamespacedName{Namespace: token.Namespace, Name: saName}, sa)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Create ServiceAccount if it doesn't exist
	if apierrors.IsNotFound(err) {
		logger.Info("Creating ServiceAccount", "name", saName)
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: token.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
		}
		if err := controllerutil.SetControllerReference(token, sa, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, sa); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
		// Requeue to wait for SA secret population
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// Wait for service account token to be populated
	var saSecretName string
	if len(sa.Secrets) > 0 {
		saSecretName = sa.Secrets[0].Name
	} else {
		// K8s 1.24+: need to find or create token secret
		saSecretName, err = r.getOrCreateServiceAccountTokenSecret(ctx, sa, token)
		if err != nil {
			logger.Info("Waiting for service account token", "serviceAccount", saName)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	}

	// Verify the secret has a token
	saSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: token.Namespace, Name: saSecretName}, saSecret); err != nil {
		return ctrl.Result{}, err
	}

	if len(saSecret.Data[corev1.ServiceAccountTokenKey]) == 0 {
		logger.Info("Service account token not yet populated, requeuing")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// Create RBAC resources
	if err := r.createRBACResources(ctx, token, saName); err != nil {
		return ctrl.Result{}, err
	}

	// Create cluster registration values secret
	if err := r.createClusterRegistrationSecret(ctx, token, saSecretName); err != nil {
		return ctrl.Result{}, err
	}

	// Update status
	token.Status.SecretName = token.Name
	token.Status.Expires = nil
	if token.Spec.TTL != nil {
		token.Status.Expires = &metav1.Time{Time: token.CreationTimestamp.Add(token.Spec.TTL.Duration)}
	}

	if err := r.Status().Update(ctx, token); err != nil {
		return ctrl.Result{}, err
	}

	// Schedule requeue if TTL is set
	if token.Spec.TTL != nil && token.Spec.TTL.Duration > 0 {
		expire := token.CreationTimestamp.Add(token.Spec.TTL.Duration)
		requeueAfter := time.Until(expire)
		if requeueAfter > 0 {
			logger.Info("Scheduling token expiration check", "after", requeueAfter)
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
	}

	return ctrl.Result{}, nil
}

// getOrCreateServiceAccountTokenSecret handles K8s 1.24+ service account token creation
func (r *ClusterRegistrationTokenReconciler) getOrCreateServiceAccountTokenSecret(ctx context.Context, sa *corev1.ServiceAccount, token *fleet.ClusterRegistrationToken) (string, error) {
	logger := log.FromContext(ctx)

	// Try to find existing token secret
	var secretList corev1.SecretList
	if err := r.List(ctx, &secretList, client.InNamespace(sa.Namespace)); err != nil {
		return "", err
	}

	// Look for a secret that references this service account
	for _, secret := range secretList.Items {
		if secret.Type == corev1.SecretTypeServiceAccountToken &&
			secret.Annotations[corev1.ServiceAccountNameKey] == sa.Name {
			if len(secret.Data[corev1.ServiceAccountTokenKey]) > 0 {
				return secret.Name, nil
			}
		}
	}

	// Create a new token secret
	secretName := names.SafeConcatName(sa.Name, "token")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: sa.Namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: sa.Name,
			},
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	if err := controllerutil.SetControllerReference(token, secret, r.Scheme); err != nil {
		return "", err
	}

	if err := r.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}

	logger.Info("Created service account token secret", "secret", secretName)
	return secretName, nil
}

// createRBACResources creates the necessary Role and RoleBinding resources
func (r *ClusterRegistrationTokenReconciler) createRBACResources(ctx context.Context, token *fleet.ClusterRegistrationToken, saName string) error {
	logger := log.FromContext(ctx)

	// Role for creating ClusterRegistrations in token namespace
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SafeConcatName(saName, "role"),
			Namespace: token.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"create"},
				APIGroups: []string{fleet.SchemeGroupVersion.Group},
				Resources: []string{fleet.ClusterRegistrationResourceNamePlural},
			},
		},
	}
	if err := controllerutil.SetControllerReference(token, role, r.Scheme); err != nil {
		return err
	}
	if err := r.createOrUpdate(ctx, role); err != nil {
		return err
	}

	// RoleBinding for the role
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SafeConcatName(saName, "to", "role"),
			Namespace: token.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: token.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     names.SafeConcatName(saName, "role"),
		},
	}
	if err := controllerutil.SetControllerReference(token, roleBinding, r.Scheme); err != nil {
		return err
	}
	if err := r.createOrUpdate(ctx, roleBinding); err != nil {
		return err
	}

	// Role for accessing secrets in system registration namespace
	credsRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SafeConcatName(saName, "creds"),
			Namespace: r.SystemRegistrationNamespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get"},
				APIGroups: []string{""},
				Resources: []string{"secrets"},
			},
		},
	}
	// Note: Cannot set owner reference across namespaces, so this resource won't have one
	if err := r.createOrUpdate(ctx, credsRole); err != nil {
		return err
	}

	// RoleBinding for creds role
	credsRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      names.SafeConcatName(saName, "creds"),
			Namespace: r.SystemRegistrationNamespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: token.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     names.SafeConcatName(saName, "creds"),
		},
	}
	// Note: Cannot set owner reference across namespaces
	if err := r.createOrUpdate(ctx, credsRoleBinding); err != nil {
		return err
	}

	logger.V(1).Info("Created/updated RBAC resources", "serviceAccount", saName)
	return nil
}

// createClusterRegistrationSecret creates the secret with cluster registration values
func (r *ClusterRegistrationTokenReconciler) createClusterRegistrationSecret(ctx context.Context, token *fleet.ClusterRegistrationToken, saSecretName string) error {
	logger := log.FromContext(ctx)

	// Get the service account secret
	saSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: token.Namespace, Name: saSecretName}, saSecret); err != nil {
		return err
	}

	// Build values map
	values := map[string]interface{}{
		"clusterNamespace":            token.Namespace,
		config.APIServerURLKey:        config.Get().APIServerURL,
		config.APIServerCAKey:         string(config.Get().APIServerCA),
		"token":                       string(saSecret.Data[corev1.ServiceAccountTokenKey]),
		"systemRegistrationNamespace": r.SystemRegistrationNamespace,
	}

	if r.SystemNamespace != config.DefaultNamespace {
		values["internal"] = map[string]interface{}{
			"systemNamespace": r.SystemNamespace,
		}
	}

	data, err := yaml.Marshal(values)
	if err != nil {
		return err
	}

	// Create or update the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      token.Name,
			Namespace: token.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Data: map[string][]byte{
			config.ImportTokenSecretValuesKey: data,
		},
		Type: "fleet.cattle.io/cluster-registration-values",
	}

	if err := controllerutil.SetControllerReference(token, secret, r.Scheme); err != nil {
		return err
	}

	if err := r.createOrUpdate(ctx, secret); err != nil {
		return err
	}

	logger.V(1).Info("Created/updated cluster registration secret", "secret", token.Name)
	return nil
}

// createOrUpdate creates or updates a resource
func (r *ClusterRegistrationTokenReconciler) createOrUpdate(ctx context.Context, obj client.Object) error {
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)

	if apierrors.IsNotFound(err) {
		return r.Create(ctx, obj)
	} else if err != nil {
		return err
	}

	// Update existing object
	obj.SetResourceVersion(existing.GetResourceVersion())
	return r.Update(ctx, obj)
}

// deleteExpired checks if the token is expired and deletes it if so
func (r *ClusterRegistrationTokenReconciler) deleteExpired(ctx context.Context, token *fleet.ClusterRegistrationToken) (bool, ctrl.Result, error) {
	ttl := token.Spec.TTL
	if ttl == nil || ttl.Duration <= 0 {
		return false, ctrl.Result{}, nil
	}

	expire := token.CreationTimestamp.Add(ttl.Duration)
	if time.Now().After(expire) {
		logger := log.FromContext(ctx)
		logger.Info("Deleting expired ClusterRegistrationToken", "namespace", token.Namespace, "name", token.Name)
		if err := r.Delete(ctx, token); err != nil && !apierrors.IsNotFound(err) {
			return true, ctrl.Result{}, err
		}
		return true, ctrl.Result{}, nil
	}

	return false, ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *ClusterRegistrationTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.ClusterRegistrationToken{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Secret{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
