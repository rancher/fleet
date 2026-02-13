package helmdeployer

import (
	"fmt"

	"helm.sh/helm/v4/pkg/release"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
)

// releaserToV1Release converts a release.Releaser interface to a concrete v1.Release type.
// This follows the same pattern used in Helm v4's own codebase for handling versioned releases.
func releaserToV1Release(rel release.Releaser) (*releasev1.Release, error) {
	switch r := rel.(type) {
	case releasev1.Release:
		return &r, nil
	case *releasev1.Release:
		return r, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported release type: %T", rel)
	}
}

// releaseListToV1List converts a slice of release.Releaser interfaces to v1.Release pointers.
func releaseListToV1List(ls []release.Releaser) ([]*releasev1.Release, error) {
	rls := make([]*releasev1.Release, 0, len(ls))
	for _, val := range ls {
		rel, err := releaserToV1Release(val)
		if err != nil {
			return nil, err
		}
		rls = append(rls, rel)
	}
	return rls, nil
}

// listReleases queries storage with a filter function and returns v1.Release pointers.
func listReleases(store *storage.Storage, filter func(*releasev1.Release) bool) ([]*releasev1.Release, error) {
	releasers, err := store.List(func(r release.Releaser) bool {
		if v1Rel, ok := r.(*releasev1.Release); ok {
			return filter(v1Rel)
		}
		return false
	})
	if err != nil {
		return nil, err
	}

	return releaseListToV1List(releasers)
}

// getReleaseHistory retrieves the history for a release name.
func getReleaseHistory(store *storage.Storage, name string) ([]*releasev1.Release, error) {
	releasers, err := store.History(name)
	if err != nil {
		return nil, err
	}

	return releaseListToV1List(releasers)
}

// getLastRelease retrieves the most recent release for a name.
func getLastRelease(store *storage.Storage, name string) (*releasev1.Release, error) {
	releaser, err := store.Last(name)
	if err != nil {
		return nil, err
	}

	return releaserToV1Release(releaser)
}

// patchReleaseStatus updates the status of a release in storage.
// This is useful for transitioning releases from transient states like "pending-install"
// to terminal states like "failed" to allow operations to proceed.
func patchReleaseStatus(store *storage.Storage, rel *releasev1.Release, newStatus releasecommon.Status) error {
	// Update the release status
	rel.Info.Status = newStatus

	// Update the release in storage
	return store.Update(rel)
}
