package options_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rancher/fleet/internal/cmd/controller/options"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestMerge_DownstreamResources(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "base-secret"},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "ConfigMap", Name: "target-cm"},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.DownstreamResources, 2)
	a.Equal(fleet.DownstreamResource{Kind: "Secret", Name: "base-secret"}, result.DownstreamResources[0])
	a.Equal(fleet.DownstreamResource{Kind: "ConfigMap", Name: "target-cm"}, result.DownstreamResources[1])

	// base and custom must remain unchanged (pure function)
	a.Len(base.DownstreamResources, 1)
	a.Len(custom.DownstreamResources, 1)
}

func TestMerge_DownstreamResources_EmptyCustom(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "base-secret"},
		},
	}

	result := options.Merge(base, fleet.BundleDeploymentOptions{})
	a.Equal(base.DownstreamResources, result.DownstreamResources)
}

func TestMerge_DownstreamResources_EmptyBase(t *testing.T) {
	a := assert.New(t)

	custom := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "target-secret"},
		},
	}

	result := options.Merge(fleet.BundleDeploymentOptions{}, custom)
	a.Equal(custom.DownstreamResources, result.DownstreamResources)
}
