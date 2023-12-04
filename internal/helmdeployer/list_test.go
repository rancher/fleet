package helmdeployer_test

import (
	"testing"

	"github.com/rancher/fleet/internal/helmdeployer"

	"github.com/stretchr/testify/assert"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
)

type fakeList struct {
	releases []*release.Release
}

func (f fakeList) Run() ([]*release.Release, error) {
	return f.releases, nil
}

func newRelease(name string, namespace string, annotations map[string]string) *release.Release {
	return &release.Release{
		Name:      name,
		Namespace: namespace,
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{
				Annotations: annotations,
			},
		},
	}
}

func TestListDeployments(t *testing.T) {
	r := assert.New(t)

	const (
		bundleIDAnnotation = "fleet.cattle.io/bundle-id"
		agentNSAnnotation  = "fleet.cattle.io/agent-namespace"
	)

	h := helmdeployer.New("cattle-fleet-test", "", "", "")

	tests := map[string]struct {
		releases             []*release.Release
		expectedBundleIDs    []string
		expectedReleaseNames []string
	}{
		"no chart has fleet annotations": {
			releases: []*release.Release{
				newRelease("test0", "any", map[string]string{}),
				newRelease("test1", "any", map[string]string{
					bundleIDAnnotation: "any",
					agentNSAnnotation:  "any",
				}),
			},
			expectedBundleIDs:    []string{},
			expectedReleaseNames: []string{},
		},
		"finds charts with fleet annotations": {
			releases: []*release.Release{
				newRelease("test1", "any", nil),
				newRelease("test2", "namespace", map[string]string{
					bundleIDAnnotation: "testID",
					agentNSAnnotation:  "cattle-fleet-test",
				}),
				newRelease("test3", "cattle-fleet-namespace", map[string]string{
					bundleIDAnnotation: "test3-id",
					agentNSAnnotation:  "cattle-fleet-test",
				}),
			},
			expectedBundleIDs:    []string{"testID", "test3-id"},
			expectedReleaseNames: []string{"namespace/test2", "cattle-fleet-namespace/test3"},
		},
		"only finds own charts": {
			releases: []*release.Release{
				newRelease("test2", "cattle-fleet-test", map[string]string{
					bundleIDAnnotation: "any",
					agentNSAnnotation:  "cattle-fleet-SYSTEM",
				}),
			},
			expectedBundleIDs:    []string{},
			expectedReleaseNames: []string{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			listAction := &fakeList{releases: test.releases}
			result, err := h.ListDeployments(listAction)
			r.NoError(err)

			r.Len(result, len(test.expectedBundleIDs))
			for _, deployedBundle := range result {
				r.Contains(test.expectedBundleIDs, deployedBundle.BundleID)
				r.Contains(test.expectedReleaseNames, deployedBundle.ReleaseName)
			}
		})
	}
}
