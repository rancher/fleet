package bundle

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type Bundle struct {
	Definition *fleet.Bundle
	Scans      []*fleet.ImageScan
	matcher    *matcher
}

func New(bundle *fleet.Bundle, imageScan ...*fleet.ImageScan) (*Bundle, error) {
	a := &Bundle{
		Definition: bundle,
		Scans:      imageScan,
	}
	return a, a.initMatcher()
}
