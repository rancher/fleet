package reconciler

import (
	"context"
	"fmt"
	"sort"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/internal/cmd/controller/options"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	maxNew = 50
)

func resetStatus(status *fleet.BundleStatus, allTargets []*target.Target) (err error) {
	status.MaxNew = maxNew
	status.Summary = fleet.BundleSummary{}
	status.PartitionStatus = nil
	status.Unavailable = 0
	status.NewlyCreated = 0
	status.Summary = target.Summary(allTargets)
	status.Unavailable = target.Unavailable(allTargets)
	status.MaxUnavailable, err = target.MaxUnavailable(allTargets)
	return err
}

func updateDisplay(status *fleet.BundleStatus) {
	status.Display.ReadyClusters = fmt.Sprintf("%d/%d",
		status.Summary.Ready,
		status.Summary.DesiredReady)
	status.Display.State = string(summary.GetSummaryState(status.Summary))
}

// setResourceKey updates status.ResourceKey from the bundle, by running helm template (does not mutate bundle)
func setResourceKey(ctx context.Context, status *fleet.BundleStatus, bundle *fleet.Bundle, manifest *manifest.Manifest, isNSed func(schema.GroupVersionKind) bool) error {
	seen := map[fleet.ResourceKey]struct{}{}

	// iterate over the defined targets, from "targets.yaml", not the
	// actually matched targets to avoid duplicates
	for i := range bundle.Spec.Targets {
		opts := options.Merge(bundle.Spec.BundleDeploymentOptions, bundle.Spec.Targets[i].BundleDeploymentOptions)
		objs, err := helmdeployer.Template(ctx, bundle.Name, manifest, opts)
		if err != nil {
			logrus.Infof("While calculating status.ResourceKey, error running helm template for bundle %s with target options from %s: %v", bundle.Name, bundle.Spec.Targets[i].Name, err)
			continue
		}

		for _, obj := range objs {
			m, err := meta.Accessor(obj)
			if err != nil {
				return err
			}
			key := fleet.ResourceKey{
				Namespace: m.GetNamespace(),
				Name:      m.GetName(),
			}
			gvk := obj.GetObjectKind().GroupVersionKind()
			if key.Namespace == "" && isNSed(gvk) {
				if opts.DefaultNamespace == "" {
					key.Namespace = "default"
				} else {
					key.Namespace = opts.DefaultNamespace
				}
			}
			key.APIVersion, key.Kind = gvk.ToAPIVersionAndKind()
			seen[key] = struct{}{}
		}
	}

	keys := []fleet.ResourceKey{}
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		keyi := keys[i]
		keyj := keys[j]
		if keyi.APIVersion != keyj.APIVersion {
			return keyi.APIVersion < keyj.APIVersion
		}
		if keyi.Kind != keyj.Kind {
			return keyi.Kind < keyj.Kind
		}
		if keyi.Namespace != keyj.Namespace {
			return keyi.Namespace < keyj.Namespace
		}
		if keyi.Name != keyj.Name {
			return keyi.Name < keyj.Name
		}
		return false
	})
	status.ResourceKey = keys

	return nil
}
