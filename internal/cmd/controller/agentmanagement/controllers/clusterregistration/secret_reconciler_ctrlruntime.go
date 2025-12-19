package clusterregistration

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// SecretReconciler reconciles registration secrets for expiration
type SecretReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	SystemRegistrationNamespace string
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;delete

// Reconcile handles Secret expiration
func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Only handle secrets in system registration namespace
	if req.Namespace != r.SystemRegistrationNamespace {
		return ctrl.Result{}, nil
	}

	var secret v1.Secret
	if err := r.Get(ctx, req.NamespacedName, &secret); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only handle secrets with cluster annotation
	if secret.Labels[fleet.ClusterAnnotation] == "" {
		return ctrl.Result{}, nil
	}

	age := time.Since(secret.CreationTimestamp.Time)
	if age > deleteSecretAfter {
		logrus.Infof("Deleting expired registration secret %s/%s", secret.Namespace, secret.Name)
		return ctrl.Result{}, r.Delete(ctx, &secret)
	}

	// Requeue to check again later
	return ctrl.Result{RequeueAfter: deleteSecretAfter - age + time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("registration-secret-expiration").
		For(&v1.Secret{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			secret, ok := object.(*v1.Secret)
			if !ok {
				return false
			}
			return secret.Namespace == r.SystemRegistrationNamespace &&
				secret.Labels[fleet.ClusterAnnotation] != ""
		})).
		Complete(r)
}
