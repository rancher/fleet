package helmdeployer

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const appConfigCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: appconfigs.stable.example.com
spec:
  group: stable.example.com
  names:
    kind: AppConfig
    plural: appconfigs
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
`

const multiDocCRDs = appConfigCRD + `---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: backends.stable.example.com
spec:
  group: stable.example.com
  names:
    kind: Backend
    plural: backends
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
`

// noKindMatchFor builds the error which Helm returns when the REST mapping for
// a rendered resource cannot be found.
func noKindMatchFor(group, kind, name string) error {
	err := &meta.NoKindMatchError{
		GroupKind:        schema.GroupKind{Group: group, Kind: kind},
		SearchedVersions: []string{"v1"},
	}

	return fmt.Errorf(
		"resource mapping not found for name: %q namespace: %q from %q: %w\nensure CRDs are installed first",
		name, "", "", err,
	)
}

// buildError wraps err the way Helm does when building the Kubernetes objects
// for a release manifest fails.
func buildError(errs ...error) error {
	var err error
	if len(errs) == 1 {
		err = errs[0]
	} else {
		err = utilerrors.NewAggregate(errs)
	}

	return fmt.Errorf("unable to build kubernetes objects from release manifest: %w", err)
}

func TestNoKindMatchErrors(t *testing.T) {
	appConfig := noKindMatchFor("stable.example.com", "AppConfig", "frontend-config")
	backend := noKindMatchFor("stable.example.com", "Backend", "frontend-backend")

	testCases := []struct {
		name          string
		err           error
		expectedKinds []schema.GroupKind
	}{
		{
			name:          "nil error",
			err:           nil,
			expectedKinds: nil,
		},
		{
			name:          "unrelated error",
			err:           errors.New("something else went wrong"),
			expectedKinds: nil,
		},
		{
			name: "single wrapped error",
			err:  buildError(appConfig),
			expectedKinds: []schema.GroupKind{
				{Group: "stable.example.com", Kind: "AppConfig"},
			},
		},
		{
			name: "aggregated errors",
			err:  buildError(appConfig, backend),
			expectedKinds: []schema.GroupKind{
				{Group: "stable.example.com", Kind: "AppConfig"},
				{Group: "stable.example.com", Kind: "Backend"},
			},
		},
		{
			name: "aggregate mixing unrelated errors",
			err:  buildError(errors.New("something else went wrong"), appConfig),
			expectedKinds: []schema.GroupKind{
				{Group: "stable.example.com", Kind: "AppConfig"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			var kinds []schema.GroupKind
			for _, err := range noKindMatchErrors(tc.err) {
				kinds = append(kinds, err.GroupKind)
			}

			assert.Equal(tc.expectedKinds, kinds)
		})
	}
}

func TestIsMissingOwnCRDsError(t *testing.T) {
	testCases := []struct {
		name           string
		crds           []*common.File
		dependencyCRDs []*common.File
		err            error
		expectedResult bool
	}{
		{
			name:           "no error",
			crds:           []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            nil,
			expectedResult: false,
		},
		{
			name:           "unrelated error",
			crds:           []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            buildError(errors.New("connection refused")),
			expectedResult: false,
		},
		{
			name:           "kind defined by the chart",
			crds:           []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            buildError(noKindMatchFor("stable.example.com", "AppConfig", "frontend-config")),
			expectedResult: true,
		},
		{
			name: "kinds from a multi document CRD file",
			crds: []*common.File{{Name: "crds/crds.yaml", Data: []byte(multiDocCRDs)}},
			err: buildError(
				noKindMatchFor("stable.example.com", "AppConfig", "frontend-config"),
				noKindMatchFor("stable.example.com", "Backend", "frontend-backend"),
			),
			expectedResult: true,
		},
		{
			name:           "kind not defined by the chart",
			crds:           []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            buildError(noKindMatchFor("stable.example.com", "AppConfigs", "frontend-config")),
			expectedResult: false,
		},
		{
			name:           "kind from another group",
			crds:           []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            buildError(noKindMatchFor("other.example.com", "AppConfig", "frontend-config")),
			expectedResult: false,
		},
		{
			name: "one kind out of two not defined by the chart",
			crds: []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err: buildError(
				noKindMatchFor("stable.example.com", "AppConfig", "frontend-config"),
				noKindMatchFor("stable.example.com", "Backend", "frontend-backend"),
			),
			expectedResult: false,
		},
		{
			name:           "chart without CRDs",
			crds:           nil,
			err:            buildError(noKindMatchFor("stable.example.com", "AppConfig", "frontend-config")),
			expectedResult: false,
		},
		{
			name:           "CRD file not in the crds directory",
			crds:           []*common.File{{Name: "templates/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            buildError(noKindMatchFor("stable.example.com", "AppConfig", "frontend-config")),
			expectedResult: false,
		},
		{
			name:           "kind defined by a dependency",
			crds:           nil,
			dependencyCRDs: []*common.File{{Name: "crds/appconfig.yaml", Data: []byte(appConfigCRD)}},
			err:            buildError(noKindMatchFor("stable.example.com", "AppConfig", "frontend-config")),
			expectedResult: true,
		},
		{
			name:           "invalid CRD file",
			crds:           []*common.File{{Name: "crds/appconfig.yaml", Data: []byte("this is: not: a CRD")}},
			err:            buildError(noKindMatchFor("stable.example.com", "AppConfig", "frontend-config")),
			expectedResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			chart := newChartWithCRDs("test-chart", tc.crds)
			if tc.dependencyCRDs != nil {
				chart.AddDependency(newChartWithCRDs("dependency", tc.dependencyCRDs))
			}

			assert.Equal(tc.expectedResult, isMissingOwnCRDsError(tc.err, chart))
		})
	}
}

func TestIsMissingOwnCRDsErrorNilChart(t *testing.T) {
	err := buildError(noKindMatchFor("stable.example.com", "AppConfig", "frontend-config"))

	assert.False(t, isMissingOwnCRDsError(err, nil))
}

func newChartWithCRDs(name string, crds []*common.File) *chartv2.Chart {
	return &chartv2.Chart{
		Metadata: &chartv2.Metadata{
			Name:    name,
			Version: "0.1.0",
		},
		Files: crds,
	}
}
