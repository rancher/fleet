package applied

import (
	"strings"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/name"

	"github.com/rancher/wrangler/v3/pkg/apply"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

type Applied struct {
	apply apply.Apply
}

// GetSetID constructs a identifier from the provided args, bundleID "fleet-agent" is special
func GetSetID(bundleID, labelPrefix, labelSuffix string) string {
	// bundle is fleet-agent bundle, we need to use setID fleet-agent-bootstrap since it was applied with import controller
	if strings.HasPrefix(bundleID, "fleet-agent") {
		if labelSuffix == "" {
			return config.AgentBootstrapConfigName
		}
		return name.SafeConcatName(config.AgentBootstrapConfigName, labelSuffix)
	}
	if labelSuffix != "" {
		return name.SafeConcatName(labelPrefix, bundleID, labelSuffix)
	}
	return name.SafeConcatName(labelPrefix, bundleID)
}

// GetLabelsAndAnnotations returns the labels and annotations, like
// "objectset.rio.cattle.io/hash" and owners, to be able to use apply.DryRun
func GetLabelsAndAnnotations(setID string, owner runtime.Object) (map[string]string, map[string]string, error) {
	return apply.GetLabelsAndAnnotations(setID, owner)
}

func NewWithClient(config *rest.Config) (*Applied, error) {
	apply, err := apply.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &Applied{
		apply: apply.WithDynamicLookup(),
	}, nil
}

// DryRun does a dry run of the apply to get the difference between the
// desired and live state. It needs a client.
// This adds the "objectset.rio.cattle.io/applied" annotation, which is used for tracking changes.
func (a *Applied) DryRun(defaultNS string, setID string, objs ...runtime.Object) (apply.Plan, error) {
	apply := a.apply.
		WithIgnorePreviousApplied().
		WithSetID(setID).
		WithDefaultNamespace(defaultNS)
	return apply.DryRun(objs...)
}
