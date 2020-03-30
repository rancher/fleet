package render

import (
	"io"

	"github.com/rancher/fleet/pkg/helm"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/patch"
)

func IsValid(name string, m *manifest.Manifest) error {
	_, err := process(name, m)
	return err
}

func process(name string, m *manifest.Manifest) (*manifest.Manifest, error) {
	m, err := patch.Process(m)
	if err != nil {
		return nil, err
	}

	return helm.Process(name, m)
}

func ToChart(name string, m *manifest.Manifest) (io.Reader, error) {
	m, err := process(name, m)
	if err != nil {
		return nil, err
	}
	return m.ToTarGZ(name)
}
