package bundle

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// Bundle struct extends the fleet.Bundle with cluster matches and ImageScan configuration
type Bundle struct {
	Definition *fleet.Bundle
	Scans      []*fleet.ImageScan
}

func New(bundle *fleet.Bundle, imageScan ...*fleet.ImageScan) (*Bundle, error) {
	a := &Bundle{
		Definition: bundle,
		Scans:      imageScan,
	}
	return a, nil
}
