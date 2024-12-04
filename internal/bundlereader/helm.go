package bundlereader

import (
	"context"
	"fmt"
	"os"

	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetManifestFromHelmChart downloads the given helm chart and creates a manifest with its contents
func GetManifestFromHelmChart(ctx context.Context, c client.Client, bd *fleet.BundleDeployment) (*manifest.Manifest, error) {
	helm := bd.Spec.Options.Helm

	if helm == nil {
		return nil, fmt.Errorf("helm options not found")
	}
	temp, err := os.MkdirTemp("", "helmapp")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)

	nsName := types.NamespacedName{Namespace: bd.Namespace, Name: bd.Spec.HelmChartOptions.SecretName}
	auth, err := ReadHelmAuthFromSecret(ctx, c, nsName)
	if err != nil {
		return nil, err
	}
	auth.InsecureSkipVerify = bd.Spec.HelmChartOptions.InsecureSkipTLSverify

	chartURL, err := ChartURL(*helm, auth)
	if err != nil {
		return nil, err
	}

	resources, err := loadDirectory(ctx, false, false, checksum(helm), temp, chartURL, helm.Version, auth)
	if err != nil {
		return nil, err
	}

	return manifest.New(resources), nil
}
