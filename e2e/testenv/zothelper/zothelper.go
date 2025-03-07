package zothelper

import (
	"fmt"
	"net"

	"github.com/rancher/fleet/e2e/testenv/infra/cmd"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

func GetOCIReference(k kubectl.Command) (string, error) {
	externalIP, err := k.Namespace(cmd.InfraNamespace).Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
	if err != nil {
		return "", err
	}
	if net.ParseIP(externalIP) == nil {
		return "", fmt.Errorf("external ip is not valid")
	}
	return fmt.Sprintf("oci://%s:8082", externalIP), err
}
