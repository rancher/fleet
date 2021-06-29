package render

import (
	"io"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundle"
	"github.com/rancher/fleet/pkg/helm"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/patch"
)

func ToChart(name string, m *manifest.Manifest, options fleet.BundleDeploymentOptions) (io.Reader, error) {
	var (
		style = bundle.DetermineStyle(m, options)
		err   error
	)

	if style.IsRawYAML() {
		var overlays []string
		if options.YAML != nil {
			overlays = options.YAML.Overlays
		}
		m, err = patch.Process(m, overlays)
		if err != nil {
			return nil, err
		}
	}

	m, err = helm.Process(name, m, style)
	if err != nil {
		return nil, err
	}

	decrypt := false
	if options.Decrypt == nil || *options.Decrypt {
		decrypt = true
	} else {
		decrypt = false
	}

	return m.ToTarGZ(decrypt)
}
