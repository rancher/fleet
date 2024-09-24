package desiredset

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

type PatchByGVK map[schema.GroupVersionKind]map[objectset.ObjectKey]string

func (p PatchByGVK) Set(gvk schema.GroupVersionKind, namespace, name, patch string) {
	d, ok := p[gvk]
	if !ok {
		d = map[objectset.ObjectKey]string{}
		p[gvk] = d
	}
	d[objectset.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}] = patch
}

type Plan struct {
	Create objectset.ObjectKeyByGVK

	// Delete contains existing objects that are not in the desired state,
	// unless their prune label is set to "false".
	Delete objectset.ObjectKeyByGVK

	// Update contains objects, already existing in the cluster, that have
	// changes. The patch would restore their desired state, as represented
	// in the helm manifest's resources passed into Plan(..., objs).
	Update PatchByGVK

	// Objects contains objects, already existing in the cluster, that have
	// valid metadata
	Objects []runtime.Object
}

func New(config *rest.Config) (*Client, error) {
	client, err := newForConfig(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Plan does a dry run of the apply to get the difference between the
// desired and live state. It needs a client.
// This adds the "objectset.rio.cattle.io/applied" annotation, which is used for tracking changes.
func (a *Client) Plan(ctx context.Context, defaultNS string, setID string, objs ...runtime.Object) (Plan, error) {
	ds := newDesiredSet(a)
	ds.setup(defaultNS, setID, objs...)
	return ds.dryRun(ctx)
}

func (a *Client) PlanDelete(ctx context.Context, defaultNS string, setID string, objs ...runtime.Object) (objectset.ObjectKeyByGVK, error) {
	ds := newDesiredSet(a)
	ds.setup(defaultNS, setID, objs...)
	return ds.dryRunDelete(ctx)
}

// GetSetID constructs a identifier from the provided args, bundleID "fleet-agent" is special
func GetSetID(bundleID, labelPrefix, labelSuffix string) string {
	// bundle is fleet-agent bundle, we need to use setID fleet-agent-bootstrap since it was applied with import controller
	if strings.HasPrefix(bundleID, "fleet-agent") {
		if labelSuffix == "" {
			return config.AgentBootstrapConfigName
		}
		return names.SafeConcatName(config.AgentBootstrapConfigName, labelSuffix)
	}
	if labelSuffix != "" {
		return names.SafeConcatName(labelPrefix, bundleID, labelSuffix)
	}
	return names.SafeConcatName(labelPrefix, bundleID)
}

// GetLabelsAndAnnotations returns the labels and annotations, like
// "objectset.rio.cattle.io/hash" and owners, to be able to use apply.DryRun
func GetLabelsAndAnnotations(setID string) (map[string]string, map[string]string, error) {
	if setID == "" {
		return nil, nil, fmt.Errorf("set ID or owner must be set")
	}

	annotations := map[string]string{
		LabelID: setID,
	}

	labels := map[string]string{
		LabelHash: objectSetHash(annotations),
	}

	return labels, annotations, nil
}
