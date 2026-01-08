package clusterregistrationtoken

import (
	"github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"
)

// RegisterControllerRuntime registers the clusterregistrationtoken controller using controller-runtime.
func RegisterControllerRuntime(mgr ctrl.Manager, systemNamespace, systemRegistrationNamespace string) error {
	if err := (&ClusterRegistrationTokenReconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		SystemNamespace:             systemNamespace,
		SystemRegistrationNamespace: systemRegistrationNamespace,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	logrus.Infof("clusterregistrationtoken controller registered")
	return nil
}
