package helmdeployer

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"helm.sh/helm/v4/pkg/action"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/kv"

	"github.com/rancher/wrangler/v3/pkg/yaml"
)

func (h *Helm) EnsureInstalled(bundleID, resourcesID string) (bool, error) {
	releaseName, version, namespace, err := getReleaseNameVersionAndNamespace(bundleID, resourcesID)
	if err != nil {
		return false, err
	}

	if _, err := h.getRelease(releaseName, namespace, version); errors.Is(err, ErrNoRelease) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// Resources returns the resources from the helm release history
func (h *Helm) Resources(bundleID, resourcesID string) (*Resources, error) {
	releaseName, version, namespace, err := getReleaseNameVersionAndNamespace(bundleID, resourcesID)
	if err != nil {
		return &Resources{}, err
	}

	release, err := h.getRelease(releaseName, namespace, version)
	if errors.Is(err, ErrNoRelease) {
		return &Resources{}, nil
	} else if err != nil {
		return nil, err
	}

	resources := &Resources{DefaultNamespace: release.Namespace}
	resources.Objects, err = ReleaseToObjects(release)
	return resources, err
}

func (h *Helm) ResourcesFromPreviousReleaseVersion(bundleID, resourcesID string) (*Resources, error) {
	releaseName, version, namespace, err := getReleaseNameVersionAndNamespace(bundleID, resourcesID)
	if err != nil {
		return &Resources{}, err
	}

	release, err := h.getRelease(releaseName, namespace, version-1)
	if errors.Is(err, ErrNoRelease) {
		return &Resources{}, nil
	} else if err != nil {
		return nil, err
	}

	resources := &Resources{DefaultNamespace: release.Namespace}
	resources.Objects, err = ReleaseToObjects(release)
	return resources, err
}

func getReleaseNameVersionAndNamespace(bundleID, resourcesID string) (string, int, string, error) {
	// When a bundle is installed a resourcesID is generated. If there is no
	// resourcesID then there isn't anything to lookup.
	if resourcesID == "" {
		return "", 0, "", ErrNoResourceID
	}
	namespace, name := kv.Split(resourcesID, "/")
	releaseName, versionStr := kv.Split(name, ":")
	version, _ := strconv.Atoi(versionStr)

	if releaseName == "" {
		releaseName = bundleID
	}

	return releaseName, version, namespace, nil
}

func (h *Helm) getRelease(releaseName, namespace string, version int) (*releasev1.Release, error) {
	hist := action.NewHistory(h.globalCfg)

	releases, err := hist.Run(releaseName)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, ErrNoRelease
	} else if err != nil {
		return nil, err
	}

	for _, releaser := range releases {
		release, err := releaserToV1Release(releaser)
		if err != nil {
			klog.V(1).InfoS("Skipping release entry with unsupported type during history lookup",
				"error", err, "releaseName", releaseName, "namespace", namespace)
			continue
		}
		if release.Name == releaseName && release.Version == version && release.Namespace == namespace {
			return release, nil
		}
	}

	return nil, ErrNoRelease
}

// ReleaseToResourceID converts a Helm release to Fleet's resource ID format.
// The resource ID uniquely identifies a release by namespace, name, and version.
func ReleaseToResourceID(release *releasev1.Release) string {
	return fmt.Sprintf("%s/%s:%d", release.Namespace, release.Name, release.Version)
}

// ReleaseToObjects parses the manifest from a Helm release and converts it to Kubernetes runtime objects.
func ReleaseToObjects(release *releasev1.Release) ([]runtime.Object, error) {
	var err error

	objs, err := yaml.ToObjects(bytes.NewBufferString(release.Manifest))
	return objs, err
}
