package bundle

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type Bundle struct {
	Definition *fleet.Bundle
	matcher    *matcher
}

func New(bundle *fleet.Bundle) (*Bundle, error) {
	a := &Bundle{
		Definition: bundle,
	}
	return a, a.initMatcher()
}
