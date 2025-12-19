package clusterregistration

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/registration"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClusterRegistrationReconciler reconciles a ClusterRegistration object
type ClusterRegistrationReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	SystemNamespace             string
	SystemRegistrationNamespace string
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusterregistrations,verbs=get;list;watch;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusterregistrations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch

// Reconcile handles ClusterRegistration requests
func (r *ClusterRegistrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr fleet.ClusterRegistration
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip if already granted
	if cr.Status.Granted {
		return ctrl.Result{}, nil
	}

	// Skip cluster management labeled resources
	if skipClusterRegistration(&cr) {
		return ctrl.Result{}, nil
	}

	// Create or get cluster
	cluster, err := r.createOrGetCluster(ctx, &cr)
	if err != nil || cluster == nil {
		return ctrl.Result{}, err
	}

	// Wait for cluster namespace to be assigned
	if cluster.Status.Namespace == "" {
		if cr.Status.ClusterName != cluster.Name {
			cr.Status.ClusterName = cluster.Name
			if err := r.Status().Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// Set cluster as owner of cluster registration
	if err := r.setClusterOwner(ctx, &cr, cluster); err != nil {
		return ctrl.Result{}, err
	}

	// Create or get service account
	saName := names.SafeConcatName(cr.Name, string(cr.UID))
	sa, err := r.getOrCreateServiceAccount(ctx, saName, cluster, &cr)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Try to get service account token
	secret, err := r.authorizeCluster(ctx, sa, cluster, &cr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to authorize cluster: %w", err)
	}
	if secret == nil {
		// Token not ready yet
		if cr.Status.ClusterName != cluster.Name {
			cr.Status.ClusterName = cluster.Name
			if err := r.Status().Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		logrus.Infof("Cluster registration '%s/%s' waiting for service account token", cr.Namespace, cr.Name)
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

	// Delete old cluster registrations with same clientID
	if err := r.deleteOldClusterRegistrations(ctx, &cr); err != nil {
		return ctrl.Result{}, err
	}

	// Grant registration and create all resources
	if err := r.grantRegistration(ctx, &cr, cluster, saName, secret); err != nil {
		return ctrl.Result{}, err
	}

	logrus.Infof("Cluster registration '%s/%s' granted for cluster '%s/%s'", cr.Namespace, cr.Name, cluster.Namespace, cluster.Name)

	return ctrl.Result{}, nil
}

func (r *ClusterRegistrationReconciler) setClusterOwner(ctx context.Context, cr *fleet.ClusterRegistration, cluster *fleet.Cluster) error {
	ownerFound := false
	for _, owner := range cr.OwnerReferences {
		if owner.Kind == "Cluster" && owner.Name == cluster.Name && owner.UID == cluster.UID {
			ownerFound = true
			break
		}
	}
	if !ownerFound {
		cr.SetOwnerReferences([]metav1.OwnerReference{
			{
				APIVersion: fleet.SchemeGroupVersion.String(),
				Kind:       "Cluster",
				Name:       cluster.Name,
				UID:        cluster.UID,
			},
		})
		return r.Update(ctx, cr)
	}
	return nil
}

func (r *ClusterRegistrationReconciler) getOrCreateServiceAccount(ctx context.Context, saName string, cluster *fleet.Cluster, cr *fleet.ClusterRegistration) (*v1.ServiceAccount, error) {
	var sa v1.ServiceAccount
	err := r.Get(ctx, types.NamespacedName{
		Namespace: cluster.Status.Namespace,
		Name:      saName,
	}, &sa)

	if err == nil {
		return &sa, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	// Create service account
	sa = *requestSA(saName, cluster, cr)
	if err := r.Create(ctx, &sa); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, err
	}

	// Fetch the created SA
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: cluster.Status.Namespace,
		Name:      saName,
	}, &sa); err != nil {
		return nil, err
	}

	return &sa, nil
}

func (r *ClusterRegistrationReconciler) authorizeCluster(ctx context.Context, sa *v1.ServiceAccount, cluster *fleet.Cluster, req *fleet.ClusterRegistration) (*v1.Secret, error) {
	var tokenSecret *v1.Secret
	var err error

	if len(sa.Secrets) != 0 {
		var secret v1.Secret
		err = r.Get(ctx, types.NamespacedName{
			Namespace: sa.Namespace,
			Name:      sa.Secrets[0].Name,
		}, &secret)
		if err == nil {
			tokenSecret = &secret
		}
	} else {
		// For newer Kubernetes versions, get or create token secret
		tokenSecret, err = r.getOrCreateServiceAccountToken(ctx, sa)
	}

	if err != nil || tokenSecret == nil {
		return nil, err
	}

	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registration.SecretName(req.Spec.ClientID, req.Spec.ClientRandom),
			Namespace: r.SystemRegistrationNamespace,
			Labels: map[string]string{
				fleet.ClusterAnnotation: cluster.Name,
				fleet.ManagedLabel:      "true",
			},
		},
		Type: AgentCredentialSecretType,
		Data: map[string][]byte{
			"token":               tokenSecret.Data["token"],
			"deploymentNamespace": []byte(cluster.Status.Namespace),
			"clusterNamespace":    []byte(cluster.Namespace),
			"clusterName":         []byte(cluster.Name),
			"systemNamespace":     []byte(r.SystemNamespace),
		},
	}, nil
}

func (r *ClusterRegistrationReconciler) getOrCreateServiceAccountToken(ctx context.Context, sa *v1.ServiceAccount) (*v1.Secret, error) {
	// List secrets in the namespace looking for the SA token
	var secretList v1.SecretList
	if err := r.List(ctx, &secretList, client.InNamespace(sa.Namespace)); err != nil {
		return nil, err
	}

	for _, secret := range secretList.Items {
		if secret.Type == v1.SecretTypeServiceAccountToken {
			if secret.Annotations[v1.ServiceAccountNameKey] == sa.Name {
				if len(secret.Data["token"]) > 0 {
					return &secret, nil
				}
			}
		}
	}

	// Create token secret if not found (K8s 1.24+)
	tokenSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-token", sa.Name),
			Namespace: sa.Namespace,
			Annotations: map[string]string{
				v1.ServiceAccountNameKey: sa.Name,
			},
		},
		Type: v1.SecretTypeServiceAccountToken,
	}

	if err := r.Create(ctx, tokenSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, err
	}

	// Wait for token to be populated
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: sa.Namespace,
		Name:      tokenSecret.Name,
	}, tokenSecret); err != nil {
		return nil, err
	}

	if len(tokenSecret.Data["token"]) == 0 {
		return nil, nil // Token not ready yet
	}

	return tokenSecret, nil
}

func (r *ClusterRegistrationReconciler) deleteOldClusterRegistrations(ctx context.Context, current *fleet.ClusterRegistration) error {
	var crList fleet.ClusterRegistrationList
	if err := r.List(ctx, &crList, client.InNamespace(current.Namespace)); err != nil {
		return err
	}

	for _, cr := range crList.Items {
		if shouldDelete(cr, *current) {
			logrus.Debugf("Deleting old clusterregistration '%s/%s'", cr.Namespace, cr.Name)
			if err := r.Delete(ctx, &cr); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (r *ClusterRegistrationReconciler) grantRegistration(ctx context.Context, cr *fleet.ClusterRegistration, cluster *fleet.Cluster, saName string, secret *v1.Secret) error {
	// Create registration secret
	if err := r.createOrUpdate(ctx, secret); err != nil {
		return err
	}

	// Create role for cluster status updates
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"patch"},
				APIGroups:     []string{fleet.SchemeGroupVersion.Group},
				Resources:     []string{fleet.ClusterResourceNamePlural + "/status"},
				ResourceNames: []string{cluster.Name},
			},
		},
	}
	if err := r.createOrUpdate(ctx, role); err != nil {
		return err
	}

	// Create role binding for cluster status
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: cluster.Status.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     cr.Name,
		},
	}
	if err := r.createOrUpdate(ctx, roleBinding); err != nil {
		return err
	}

	// Create role binding for bundle deployments
	bundleRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cluster.Status.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: cluster.Status.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     resources.BundleDeploymentClusterRole,
		},
	}
	if err := r.createOrUpdate(ctx, bundleRoleBinding); err != nil {
		return err
	}

	// Create cluster role binding for content
	contentCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: names.SafeConcatName(cr.Name, "content"),
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: cluster.Status.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     resources.ContentClusterRole,
		},
	}
	if err := r.createOrUpdate(ctx, contentCRB); err != nil {
		return err
	}

	// Update status to granted
	cr.Status.ClusterName = cluster.Name
	cr.Status.Granted = true
	return r.Status().Update(ctx, cr)
}

func (r *ClusterRegistrationReconciler) createOrUpdate(ctx context.Context, obj client.Object) error {
	err := r.Create(ctx, obj)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}

	// Get existing and update
	key := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}

	switch o := obj.(type) {
	case *v1.Secret:
		var existing v1.Secret
		if err := r.Get(ctx, key, &existing); err != nil {
			return err
		}
		o.ResourceVersion = existing.ResourceVersion
		return r.Update(ctx, o)
	case *rbacv1.Role:
		var existing rbacv1.Role
		if err := r.Get(ctx, key, &existing); err != nil {
			return err
		}
		o.ResourceVersion = existing.ResourceVersion
		return r.Update(ctx, o)
	case *rbacv1.RoleBinding:
		var existing rbacv1.RoleBinding
		if err := r.Get(ctx, key, &existing); err != nil {
			return err
		}
		o.ResourceVersion = existing.ResourceVersion
		return r.Update(ctx, o)
	case *rbacv1.ClusterRoleBinding:
		var existing rbacv1.ClusterRoleBinding
		if err := r.Get(ctx, types.NamespacedName{Name: obj.GetName()}, &existing); err != nil {
			return err
		}
		o.ResourceVersion = existing.ResourceVersion
		return r.Update(ctx, o)
	}

	return nil
}

func (r *ClusterRegistrationReconciler) createOrGetCluster(ctx context.Context, cr *fleet.ClusterRegistration) (*fleet.Cluster, error) {
	// Try to find existing cluster by clientID
	var clusterList fleet.ClusterList
	if err := r.List(ctx, &clusterList, client.InNamespace(cr.Namespace)); err != nil {
		return nil, err
	}

	for _, cluster := range clusterList.Items {
		if cluster.Spec.ClientID == cr.Spec.ClientID {
			return &cluster, nil
		}
	}

	// Create new cluster
	clusterName := names.SafeConcatName("cluster", names.KeyHash(cr.Spec.ClientID))

	var cluster fleet.Cluster
	err := r.Get(ctx, types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      clusterName,
	}, &cluster)

	if err == nil {
		if cluster.Spec.ClientID != cr.Spec.ClientID {
			return nil, fmt.Errorf("non-matching ClientID on cluster %s/%s", cr.Namespace, clusterName)
		}
		return &cluster, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	// Create cluster
	labels := map[string]string{}
	if !config.Get().IgnoreClusterRegistrationLabels {
		for k, v := range cr.Spec.ClusterLabels {
			labels[k] = v
		}
	}
	labels[fleet.ClusterAnnotation] = clusterName

	cluster = fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: fleet.ClusterSpec{
			ClientID: cr.Spec.ClientID,
		},
	}

	if err := r.Create(ctx, &cluster); err != nil {
		if apierrors.IsAlreadyExists(err) {
			if err := r.Get(ctx, types.NamespacedName{
				Namespace: cr.Namespace,
				Name:      clusterName,
			}, &cluster); err != nil {
				return nil, err
			}
			return &cluster, nil
		}
		return nil, err
	}

	logrus.Infof("Created cluster %s/%s for registration %s", cr.Namespace, clusterName, cr.Name)
	return &cluster, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *ClusterRegistrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("clusterregistration").
		For(&fleet.ClusterRegistration{}).
		Owns(&v1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(
			&fleet.Cluster{},
			ctrlhandler.EnqueueRequestsFromMapFunc(r.findClusterRegistrationsForCluster),
		).
		Watches(
			&v1.ServiceAccount{},
			ctrlhandler.EnqueueRequestsFromMapFunc(r.findClusterRegistrationsForServiceAccount),
		).
		Complete(r)
}

func (r *ClusterRegistrationReconciler) findClusterRegistrationsForCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	cluster, ok := obj.(*fleet.Cluster)
	if !ok || cluster.Status.Namespace == "" {
		return nil
	}

	// Find ClusterRegistrations with matching clientID
	var crList fleet.ClusterRegistrationList
	if err := r.List(ctx, &crList, client.InNamespace(cluster.Namespace)); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, cr := range crList.Items {
		if cr.Spec.ClientID == cluster.Spec.ClientID && !cr.Status.Granted {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: cr.Namespace,
					Name:      cr.Name,
				},
			})
			logrus.Infof("Cluster '%s/%s' namespace assigned, enqueueing registration '%s/%s'",
				cluster.Namespace, cluster.Name, cr.Namespace, cr.Name)
		}
	}

	return requests
}

func (r *ClusterRegistrationReconciler) findClusterRegistrationsForServiceAccount(ctx context.Context, obj client.Object) []reconcile.Request {
	sa, ok := obj.(*v1.ServiceAccount)
	if !ok {
		return nil
	}

	ns := sa.Annotations[fleet.ClusterRegistrationNamespaceAnnotation]
	name := sa.Annotations[fleet.ClusterRegistrationAnnotation]
	if ns == "" || name == "" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      name,
			},
		},
	}
}
