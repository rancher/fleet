package helmdeployer

import (
	"bytes"
	"errors"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// isMissingOwnCRDsError reports whether err consists solely of REST mapping
// failures for kinds which the chart itself declares in its crds/ directory.
//
// A server-side dry run cannot install those CRDs, hence custom resources
// defined by the chart have no REST mapping yet and Helm fails to build the
// Kubernetes objects for the rendered manifest. The actual install installs
// the contents of crds/ first, so it does not suffer from this limitation.
func isMissingOwnCRDsError(err error, chart *chartv2.Chart) bool {
	missing := noKindMatchErrors(err)
	if len(missing) == 0 {
		return false
	}

	declared := chartCRDGroupKinds(chart)
	for _, m := range missing {
		if !declared.Has(m.GroupKind) {
			return false
		}
	}

	return true
}

// noKindMatchErrors collects all the NoKindMatchErrors contained in err.
// Errors reported while building a resource list are wrapped into an aggregate
// when more than one resource fails, and aggregates cannot be traversed by
// errors.As, hence the explicit recursion.
func noKindMatchErrors(err error) []*meta.NoKindMatchError {
	var result []*meta.NoKindMatchError

	var collect func(error)
	collect = func(err error) {
		if err == nil {
			return
		}

		var noKindMatch *meta.NoKindMatchError
		if errors.As(err, &noKindMatch) {
			result = append(result, noKindMatch)
			return
		}

		var aggregate utilerrors.Aggregate
		if errors.As(err, &aggregate) {
			for _, e := range aggregate.Errors() {
				collect(e)
			}
		}
	}
	collect(err)

	return result
}

// chartCRDGroupKinds returns the group kinds defined by the CRDs found in the
// crds/ directory of the chart and of its dependencies.
func chartCRDGroupKinds(chart *chartv2.Chart) sets.Set[schema.GroupKind] {
	groupKinds := sets.New[schema.GroupKind]()
	if chart == nil {
		return groupKinds
	}

	for _, crd := range chart.CRDObjects() {
		if crd.File == nil {
			continue
		}

		// CRD files may contain multiple documents.
		decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(crd.File.Data))
		for {
			var def apiextv1.CustomResourceDefinition
			if err := decoder.Decode(&def); err != nil {
				// Stop at the first unreadable document: an incomplete list of
				// group kinds can only make the caller stricter, and Helm
				// reports invalid CRDs when it installs them for real.
				break
			}
			if def.Spec.Group == "" || def.Spec.Names.Kind == "" {
				continue
			}
			groupKinds.Insert(schema.GroupKind{Group: def.Spec.Group, Kind: def.Spec.Names.Kind})
		}
	}

	return groupKinds
}
