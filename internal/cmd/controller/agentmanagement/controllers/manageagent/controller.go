package manageagent

import (
	"github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"
)

// RegisterControllerRuntime registers manageagent controllers using controller-runtime.
func RegisterControllerRuntime(mgr ctrl.Manager, systemNamespace string) error {
	if err := (&ManageAgentReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: systemNamespace,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&ClusterStatusReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: systemNamespace,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	logrus.Infof("manageagent controllers registered")
	return nil
}
