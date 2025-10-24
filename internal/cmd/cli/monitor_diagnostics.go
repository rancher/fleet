package cli

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// checkContentIssues identifies BundleDeployments with missing or problematic Content resources
func (m *Monitor) checkContentIssues(bundleDeployments []fleet.BundleDeployment, contents []fleet.Content) []ContentIssue {
	var issues []ContentIssue

	contentMap := make(map[string]fleet.Content)
	for _, content := range contents {
		contentMap[content.Name] = content
	}

	for _, bd := range bundleDeployments {
		issue := ContentIssue{
			Namespace: bd.Namespace,
			Name:      bd.Name,
			Issues:    []string{},
		}

		hasIssue := false

		// Check spec.deploymentID content
		if bd.Spec.DeploymentID != "" {
			contentName := extractContentName(bd.Spec.DeploymentID)
			issue.ContentName = contentName

			content, exists := contentMap[contentName]
			issue.ContentExists = exists

			if !exists {
				issue.Issues = append(issue.Issues, "content_not_found")
				hasIssue = true
			} else if content.DeletionTimestamp != nil {
				ts := content.DeletionTimestamp.UTC().Format(time.RFC3339)
				issue.ContentDeletionTimestamp = &ts
				issue.Issues = append(issue.Issues, "content_has_deletion_timestamp")
				hasIssue = true
			}
		}

		// Check spec.stagedDeploymentID content
		if bd.Spec.StagedDeploymentID != "" {
			stagedContentName := extractContentName(bd.Spec.StagedDeploymentID)
			if stagedContentName != issue.ContentName {
				issue.StagedContentName = stagedContentName
				_, exists := contentMap[stagedContentName]
				issue.StagedContentExists = &exists

				if !exists {
					issue.Issues = append(issue.Issues, "staged_content_not_found")
					hasIssue = true
				}
			}
		}

		// Check status.appliedDeploymentID content
		if bd.Status.AppliedDeploymentID != "" {
			appliedContentName := extractContentName(bd.Status.AppliedDeploymentID)
			if appliedContentName != issue.ContentName {
				issue.AppliedContentName = appliedContentName
				_, exists := contentMap[appliedContentName]
				issue.AppliedContentExists = &exists

				if !exists {
					issue.Issues = append(issue.Issues, "applied_content_not_found")
					hasIssue = true
				}
			}
		}

		if hasIssue {
			issues = append(issues, issue)
		}
	}

	return issues
}

// extractContentName extracts the content name from a deploymentID
func extractContentName(deploymentID string) string {
	// deploymentID format is "s-<sha256>:options" or just "s-<sha256>"
	parts := strings.SplitN(deploymentID, ":", 2)
	return parts[0]
}

// checkAPIConsistency tests for API server cache inconsistencies (time travel)
func (m *Monitor) checkAPIConsistency(ctx context.Context, c client.Client) (*APIConsistency, error) {
	// Test by fetching the namespace multiple times and checking if resourceVersion changes unexpectedly
	namespace := &corev1.Namespace{}
	versions := make([]string, 3)

	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		err := c.Get(ctx, client.ObjectKey{Name: m.Namespace}, namespace)
		if err != nil {
			return nil, err
		}
		versions[i] = namespace.ResourceVersion
	}

	consistent := versions[0] == versions[1] && versions[1] == versions[2]

	return &APIConsistency{
		Consistent: consistent,
		Versions:   versions,
	}, nil
}

// getRecentEvents retrieves recent Fleet-related Kubernetes events
func (m *Monitor) getRecentEvents(ctx context.Context, c client.Client) ([]corev1.Event, error) {
	eventList := &corev1.EventList{}
	err := c.List(ctx, eventList)
	if err != nil {
		return nil, err
	}

	// Filter for Fleet-related events
	var fleetEvents []corev1.Event
	for _, event := range eventList.Items {
		if event.InvolvedObject.APIVersion == "fleet.cattle.io/v1alpha1" {
			fleetEvents = append(fleetEvents, event)
		}
	}

	// Return last 20 events
	if len(fleetEvents) > 20 {
		fleetEvents = fleetEvents[len(fleetEvents)-20:]
	}

	return fleetEvents, nil
}

// collectDiagnostics gathers all diagnostic checks and returns a comprehensive diagnostics report
func (m *Monitor) collectDiagnostics(gitRepos []fleet.GitRepo, bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment, contents []fleet.Content, clusters []fleet.Cluster, clusterGroups []fleet.ClusterGroup, orphanedSecrets []corev1.Secret, contentIssues []ContentIssue) *Diagnostics {
	// Extract secrets with invalid owners from orphaned secrets (UID mismatches)
	invalidSecretOwners := m.filterSecretsWithInvalidOwners(orphanedSecrets)

	gitRepoBundleInconsistencies := m.detectGitRepoBundleInconsistencies(gitRepos, bundles)
	resourcesWithMultipleFinalizers := m.detectMultipleFinalizers(gitRepos, bundles, bundleDeployments)

	return &Diagnostics{
		StuckBundleDeployments:                 m.convertBundleDeployments(m.detectStuckBundleDeployments(bundleDeployments)),
		GitRepoBundleInconsistencies:           m.convertBundles(gitRepoBundleInconsistencies),
		InvalidSecretOwners:                    m.convertSecrets(invalidSecretOwners),
		ResourcesWithMultipleFinalizers:        resourcesWithMultipleFinalizers,
		LargeBundles:                           m.convertBundles(m.detectLargeBundles(bundles, contents)),
		BundlesWithMissingContent:              m.convertBundles(m.detectBundlesWithMissingContent(bundles, contents)),
		BundlesWithNoDeployments:               m.convertBundles(m.detectBundlesWithNoDeployments(bundles, bundleDeployments)),
		GitReposWithNoBundles:                  m.convertGitRepos(m.detectGitReposWithNoBundles(gitRepos, bundles)),
		ClustersWithAgentIssues:                m.convertClusters(m.detectClustersWithAgentIssues(clusters)),
		ClusterGroupsWithNoClusters:            m.convertClusterGroups(m.detectClusterGroupsWithNoClusters(clusterGroups)),
		BundlesWithMissingGitRepo:              m.convertBundles(m.detectBundlesWithMissingGitRepo(bundles, gitRepos)),
		BundleDeploymentsWithMissingBundle:     m.convertBundleDeployments(m.detectBundleDeploymentsWithMissingBundle(bundleDeployments, bundles)),
		GitReposWithGenerationMismatch:         m.convertGitRepos(m.detectGitReposWithGenerationMismatch(gitRepos)),
		BundlesWithGenerationMismatch:          m.convertBundles(m.detectBundlesWithGenerationMismatch(bundles)),
		BundleDeploymentsWithSyncGenerationMismatch: m.convertBundleDeployments(m.detectBundleDeploymentsWithSyncGenerationMismatch(bundleDeployments)),
		OrphanedSecretsCount:                   len(orphanedSecrets),
		InvalidSecretOwnersCount:               len(invalidSecretOwners),
		ContentIssuesCount:                     len(contentIssues),
		GitRepoBundleInconsistenciesCount:      len(gitRepoBundleInconsistencies),
		ResourcesWithMultipleFinalizersCount:   len(resourcesWithMultipleFinalizers),
		BundlesWithDeletionTimestamp:           m.countBundlesWithDeletionTimestamp(bundles),
		BundleDeploymentsWithDeletionTimestamp: m.countBundleDeploymentsWithDeletionTimestamp(bundleDeployments),
		ContentsWithDeletionTimestamp:          m.countContentsWithDeletionTimestamp(contents),
	}
}



// detectStuckBundleDeployments identifies bundledeployments stuck due to various issues
func (m *Monitor) detectStuckBundleDeployments(bundleDeployments []fleet.BundleDeployment) []fleet.BundleDeployment {
	var stuckBundleDeployments []fleet.BundleDeployment
	for _, bd := range bundleDeployments {
		stuck := false

		// Check if being deleted but not removed yet (finalizers blocking)
		if bd.DeletionTimestamp != nil {
			stuck = true
		}

		// Check if agent hasn't applied the target deploymentID yet
		// This is the primary indicator of a stuck deployment
		if bd.Spec.DeploymentID != bd.Status.AppliedDeploymentID {
			stuck = true
		}

		// Check if forceSyncGeneration mismatch - means forced sync hasn't been applied
		// Note: syncGeneration tracks forceSyncGeneration, NOT resource generation
		if bd.Spec.Options.ForceSyncGeneration > 0 {
			if bd.Status.SyncGeneration == nil || *bd.Status.SyncGeneration != bd.Spec.Options.ForceSyncGeneration {
				stuck = true
			}
		}

		if stuck {
			stuckBundleDeployments = append(stuckBundleDeployments, bd)
		}
	}
	return stuckBundleDeployments
}

// detectGitRepoBundleInconsistencies finds bundles with outdated commits or forceSyncGeneration
func (m *Monitor) detectGitRepoBundleInconsistencies(gitRepos []fleet.GitRepo, bundles []fleet.Bundle) []fleet.Bundle {
	var inconsistentBundles []fleet.Bundle
	for _, bundle := range bundles {
		repoName, ok := bundle.Labels["fleet.cattle.io/repo-name"]
		if !ok {
			continue
		}
		var gitRepo *fleet.GitRepo
		for _, r := range gitRepos {
			if r.Name == repoName && r.Namespace == bundle.Namespace {
				gitRepo = &r
				break
			}
		}
		if gitRepo == nil {
			continue
		}

		inconsistent := false
		if bundle.Labels["fleet.cattle.io/commit"] != gitRepo.Status.Commit {
			inconsistent = true
		}
		if bundle.Spec.ForceSyncGeneration != gitRepo.Spec.ForceSyncGeneration {
			inconsistent = true
		}

		if inconsistent {
			inconsistentBundles = append(inconsistentBundles, bundle)
		}
	}
	return inconsistentBundles
}

// filterSecretsWithInvalidOwners extracts secrets from orphaned list that have owner UID mismatches
// (excludes secrets with deletion timestamps or missing owners)
func (m *Monitor) filterSecretsWithInvalidOwners(orphanedSecrets []corev1.Secret) []corev1.Secret {
	var invalidOwners []corev1.Secret

	for _, secret := range orphanedSecrets {
		// Skip secrets with deletion timestamps - those are just being deleted
		if secret.DeletionTimestamp != nil {
			continue
		}

		// Skip secrets with no owners - those are missing owners, not invalid
		if len(secret.OwnerReferences) == 0 {
			continue
		}

		// This secret has an owner reference but is orphaned, so it's an invalid owner (UID mismatch)
		invalidOwners = append(invalidOwners, secret)
	}

	return invalidOwners
}

// countBundlesWithDeletionTimestamp counts bundles that have deletion timestamps
func (m *Monitor) countBundlesWithDeletionTimestamp(bundles []fleet.Bundle) int {
	count := 0
	for _, bundle := range bundles {
		if bundle.DeletionTimestamp != nil {
			count++
		}
	}
	return count
}

// countBundleDeploymentsWithDeletionTimestamp counts bundledeployments with deletion timestamps
func (m *Monitor) countBundleDeploymentsWithDeletionTimestamp(bundleDeployments []fleet.BundleDeployment) int {
	count := 0
	for _, bd := range bundleDeployments {
		if bd.DeletionTimestamp != nil {
			count++
		}
	}
	return count
}

// countContentsWithDeletionTimestamp counts contents with deletion timestamps
func (m *Monitor) countContentsWithDeletionTimestamp(contents []fleet.Content) int {
	count := 0
	for _, content := range contents {
		if content.DeletionTimestamp != nil {
			count++
		}
	}
	return count
}

// detectMultipleFinalizers identifies resources with more than one finalizer (indicates potential bugs)
func (m *Monitor) detectMultipleFinalizers(gitRepos []fleet.GitRepo, bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment) []ResourceWithFinalizers {
	var result []ResourceWithFinalizers

	// Check GitRepos
	for _, gr := range gitRepos {
		if len(gr.Finalizers) > 1 {
			info := ResourceWithFinalizers{
				Kind:           "GitRepo",
				Namespace:      gr.Namespace,
				Name:           gr.Name,
				Finalizers:     gr.Finalizers,
				FinalizerCount: len(gr.Finalizers),
			}
			if gr.DeletionTimestamp != nil {
				ts := gr.DeletionTimestamp.UTC().Format(time.RFC3339)
				info.DeletionTimestamp = &ts
			}
			result = append(result, info)
		}
	}

	// Check Bundles
	for _, bundle := range bundles {
		if len(bundle.Finalizers) > 1 {
			info := ResourceWithFinalizers{
				Kind:           "Bundle",
				Namespace:      bundle.Namespace,
				Name:           bundle.Name,
				Finalizers:     bundle.Finalizers,
				FinalizerCount: len(bundle.Finalizers),
			}
			if bundle.DeletionTimestamp != nil {
				ts := bundle.DeletionTimestamp.UTC().Format(time.RFC3339)
				info.DeletionTimestamp = &ts
			}
			result = append(result, info)
		}
	}

	// Check BundleDeployments
	for _, bd := range bundleDeployments {
		if len(bd.Finalizers) > 1 {
			info := ResourceWithFinalizers{
				Kind:           "BundleDeployment",
				Namespace:      bd.Namespace,
				Name:           bd.Name,
				Finalizers:     bd.Finalizers,
				FinalizerCount: len(bd.Finalizers),
			}
			if bd.DeletionTimestamp != nil {
				ts := bd.DeletionTimestamp.UTC().Format(time.RFC3339)
				info.DeletionTimestamp = &ts
			}
			result = append(result, info)
		}
	}

	// Only Contents are allowed to have multiple finalizers for ref counting
	// We skip them intentionally

	return result
}

// detectLargeBundles detects bundles larger than 1MB (etcd performance threshold)
func (m *Monitor) detectLargeBundles(bundles []fleet.Bundle, contents []fleet.Content) []fleet.Bundle {
	var largeBundles []fleet.Bundle
	const oneMB = 1024 * 1024

	for _, bundle := range bundles {
		// Compute bundle size by marshaling the entire Bundle resource to JSON
		if bundleJSON, err := json.Marshal(bundle); err == nil {
			size := int64(len(bundleJSON))
			if size > oneMB {
				largeBundles = append(largeBundles, bundle)
			}
		}
	}

	return largeBundles
}

// detectBundlesWithMissingContent detects bundles with resourcesSHA256Sum but no corresponding Content
func (m *Monitor) detectBundlesWithMissingContent(bundles []fleet.Bundle, contents []fleet.Content) []fleet.Bundle {
	var missingContent []fleet.Bundle

	// Create set of content names
	contentNames := make(map[string]bool)
	for _, content := range contents {
		contentNames[content.Name] = true
	}

	for _, bundle := range bundles {
		if bundle.Status.ResourcesSHA256Sum != "" {
			// Content names are truncated to 63 chars
			prefix := "s-" + bundle.Status.ResourcesSHA256Sum
			if len(prefix) > 63 {
				prefix = prefix[:63]
			}

			if !contentNames[prefix] {
				missingContent = append(missingContent, bundle)
			}
		}
	}

	return missingContent
}

// detectBundlesWithNoDeployments detects bundles with zero BundleDeployments (target matching issue)
func (m *Monitor) detectBundlesWithNoDeployments(bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment) []fleet.Bundle {
	var bundlesWithNoDeployments []fleet.Bundle

	// Count deployments per bundle using bundle-name and bundle-namespace labels
	bundleDeploymentCounts := make(map[string]int)
	for _, bd := range bundleDeployments {
		bundleName := bd.Labels["fleet.cattle.io/bundle-name"]
		bundleNamespace := bd.Labels["fleet.cattle.io/bundle-namespace"]
		if bundleName != "" && bundleNamespace != "" {
			key := bundleNamespace + "/" + bundleName
			bundleDeploymentCounts[key]++
		}
	}

	for _, bundle := range bundles {
		key := bundle.Namespace + "/" + bundle.Name
		if bundleDeploymentCounts[key] == 0 {
			bundlesWithNoDeployments = append(bundlesWithNoDeployments, bundle)
		}
	}

	return bundlesWithNoDeployments
}

// detectGitReposWithNoBundles detects GitRepos with zero Bundles created
func (m *Monitor) detectGitReposWithNoBundles(gitRepos []fleet.GitRepo, bundles []fleet.Bundle) []fleet.GitRepo {
	var gitReposWithNoBundles []fleet.GitRepo

	// Count bundles per gitrepo
	bundleCounts := make(map[string]int)
	for _, bundle := range bundles {
		repoName := bundle.Labels["fleet.cattle.io/repo-name"]
		if repoName != "" {
			key := bundle.Namespace + "/" + repoName
			bundleCounts[key]++
		}
	}

	for _, gitRepo := range gitRepos {
		key := gitRepo.Namespace + "/" + gitRepo.Name
		if bundleCounts[key] == 0 {
			gitReposWithNoBundles = append(gitReposWithNoBundles, gitRepo)
		}
	}

	return gitReposWithNoBundles
}

// detectClustersWithAgentIssues detects clusters with agent connectivity or health issues
func (m *Monitor) detectClustersWithAgentIssues(clusters []fleet.Cluster) []fleet.Cluster {
	var clustersWithIssues []fleet.Cluster
	dayAgo := time.Now().Add(-24 * time.Hour)

	for _, cluster := range clusters {
		hasIssue := false

		// Check if agent is not ready
		ready := false
		for _, cond := range cluster.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}
		if !ready {
			hasIssue = true
		}

		// Check if LastSeen is missing or stale
		if cluster.Status.Agent.LastSeen.IsZero() {
			hasIssue = true
		} else if cluster.Status.Agent.LastSeen.Time.Before(dayAgo) {
			hasIssue = true
		}

		if hasIssue {
			clustersWithIssues = append(clustersWithIssues, cluster)
		}
	}

	return clustersWithIssues
}

// detectClusterGroupsWithNoClusters detects ClusterGroups with zero matched clusters
func (m *Monitor) detectClusterGroupsWithNoClusters(clusterGroups []fleet.ClusterGroup) []fleet.ClusterGroup {
	var clusterGroupsWithNoClusters []fleet.ClusterGroup

	for _, cg := range clusterGroups {
		if cg.Status.ClusterCount == 0 {
			clusterGroupsWithNoClusters = append(clusterGroupsWithNoClusters, cg)
		}
	}

	return clusterGroupsWithNoClusters
}

// detectBundlesWithMissingGitRepo detects Bundles with missing GitRepo owner reference
func (m *Monitor) detectBundlesWithMissingGitRepo(bundles []fleet.Bundle, gitRepos []fleet.GitRepo) []fleet.Bundle {
	var bundlesWithMissingGitRepo []fleet.Bundle

	// Create map of gitrepo names
	gitRepoExists := make(map[string]bool)
	for _, gr := range gitRepos {
		key := gr.Namespace + "/" + gr.Name
		gitRepoExists[key] = true
	}

	for _, bundle := range bundles {
		repoName := bundle.Labels["fleet.cattle.io/repo-name"]
		if repoName != "" {
			key := bundle.Namespace + "/" + repoName
			if !gitRepoExists[key] {
				bundlesWithMissingGitRepo = append(bundlesWithMissingGitRepo, bundle)
			}
		}
	}

	return bundlesWithMissingGitRepo
}

// detectBundleDeploymentsWithMissingBundle detects orphaned BundleDeployments
func (m *Monitor) detectBundleDeploymentsWithMissingBundle(bundleDeployments []fleet.BundleDeployment, bundles []fleet.Bundle) []fleet.BundleDeployment {
	var orphanedBundleDeployments []fleet.BundleDeployment

	// Create map of bundle names
	bundleExists := make(map[string]bool)
	for _, bundle := range bundles {
		key := bundle.Namespace + "/" + bundle.Name
		bundleExists[key] = true
	}

	for _, bd := range bundleDeployments {
		bundleName := bd.Labels["fleet.cattle.io/bundle-name"]
		bundleNamespace := bd.Labels["fleet.cattle.io/bundle-namespace"]
		if bundleName != "" && bundleNamespace != "" {
			key := bundleNamespace + "/" + bundleName
			if !bundleExists[key] {
				orphanedBundleDeployments = append(orphanedBundleDeployments, bd)
			}
		}
	}

	return orphanedBundleDeployments
}

// detectGitReposWithGenerationMismatch detects GitRepos with generation != observedGeneration
func (m *Monitor) detectGitReposWithGenerationMismatch(gitRepos []fleet.GitRepo) []fleet.GitRepo {
	var mismatchedGitRepos []fleet.GitRepo

	for _, gr := range gitRepos {
		if gr.Generation != gr.Status.ObservedGeneration {
			mismatchedGitRepos = append(mismatchedGitRepos, gr)
		}
	}

	return mismatchedGitRepos
}

// detectBundlesWithGenerationMismatch detects Bundles with generation != observedGeneration
func (m *Monitor) detectBundlesWithGenerationMismatch(bundles []fleet.Bundle) []fleet.Bundle {
	var mismatchedBundles []fleet.Bundle

	for _, bundle := range bundles {
		if bundle.Generation != bundle.Status.ObservedGeneration {
			mismatchedBundles = append(mismatchedBundles, bundle)
		}
	}

	return mismatchedBundles
}

// detectBundleDeploymentsWithSyncGenerationMismatch detects BundleDeployments with syncGeneration != forceSyncGeneration
func (m *Monitor) detectBundleDeploymentsWithSyncGenerationMismatch(bundleDeployments []fleet.BundleDeployment) []fleet.BundleDeployment {
	var mismatchedBundleDeployments []fleet.BundleDeployment

	for _, bd := range bundleDeployments {
		if bd.Spec.Options.ForceSyncGeneration > 0 {
			if bd.Status.SyncGeneration == nil || *bd.Status.SyncGeneration != bd.Spec.Options.ForceSyncGeneration {
				mismatchedBundleDeployments = append(mismatchedBundleDeployments, bd)
			}
		}
	}

	return mismatchedBundleDeployments
}
