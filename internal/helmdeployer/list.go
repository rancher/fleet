package helmdeployer

import (
	"strconv"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
)

type ListAction interface {
	Run() ([]*release.Release, error)
}

func (h *Helm) NewListAction() *action.List {
	list := action.NewList(&h.globalCfg)
	list.All = true
	return list
}

// ListDeployments returns a list of deployedBundles by listing all helm releases via
// helm's storage driver (secrets)
// It only returns deployedBundles for helm releases which have the
// "fleet.cattle.io/bundle-id" annotation.
func (h *Helm) ListDeployments(list ListAction) ([]DeployedBundle, error) {
	releases, err := list.Run()
	if err != nil {
		return nil, err
	}

	var (
		result []DeployedBundle
	)

	for _, release := range releases {
		// skip releases that don't have the bundleID annotation
		d := release.Chart.Metadata.Annotations[BundleIDAnnotation]
		if d == "" {
			continue
		}
		ns := release.Chart.Metadata.Annotations[AgentNamespaceAnnotation]
		// skip releases that don't have the agentNamespace annotation
		if ns == "" {
			continue
		}
		// skip releases from other agents
		if ns != h.agentNamespace {
			continue
		}
		// ignore error as keepResources should be false if annotation not found
		keepResources, _ := strconv.ParseBool(release.Chart.Metadata.Annotations[KeepResourcesAnnotation])
		result = append(result, DeployedBundle{
			BundleID:      d,
			ReleaseName:   release.Namespace + "/" + release.Name,
			KeepResources: keepResources,
		})
	}

	return result, nil
}
