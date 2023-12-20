// Package connection provides a connection to a Kubernetes cluster, used when importing a cluster.
package connection

import (
	"k8s.io/client-go/kubernetes"
)

func SmokeTestKubeClientConnection(client *kubernetes.Clientset) error {
	_, err := client.Discovery().ServerVersion()
	return err
}
