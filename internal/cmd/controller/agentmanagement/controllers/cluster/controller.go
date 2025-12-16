package cluster

import (
	"github.com/sirupsen/logrus"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Register registers all cluster-related controllers using controller-runtime.
func Register(mgr ctrl.Manager, systemNamespace string) error {
	// Cluster status/namespace reconciler
	if err := (&ClusterReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr); err != nil {
		return err
	}

	// Legacy namespace creation reconciler
	if err := (&clusterReconciler{}).SetupWithManager(mgr); err != nil {
		return err
	}

	// BundleDeployment enqueuer to trigger Cluster reconcile
	if err := (&bundleDeploymentEnqueuer{}).SetupWithManager(mgr); err != nil {
		return err
	}

	// Cluster import reconciler (creates downstream agent resources)
	if err := (&ClusterImportReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: systemNamespace,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	logrus.Infof("cluster controllers registered")
	return nil
}
