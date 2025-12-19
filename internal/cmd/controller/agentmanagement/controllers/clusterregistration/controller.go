package clusterregistration

import (
	"github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"
)

// RegisterControllerRuntime registers clusterregistration controllers using controller-runtime.
func RegisterControllerRuntime(mgr ctrl.Manager, systemNamespace, systemRegistrationNamespace string) error {
	if err := (&ClusterRegistrationReconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		SystemNamespace:             systemNamespace,
		SystemRegistrationNamespace: systemRegistrationNamespace,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&SecretReconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		SystemRegistrationNamespace: systemRegistrationNamespace,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	logrus.Infof("clusterregistration controllers registered")
	return nil
}
