package troubleshooting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	APIConsistencyRetries = 3
	RecentEventsCount     = 20
)

// Collector gathers diagnostic Fleet resource data from a Kubernetes cluster.
type Collector struct {
	// SystemNamespace is the Fleet system namespace (e.g. cattle-fleet-system).
	SystemNamespace string
	// Namespace is the namespace to scope cluster and cluster group queries.
	Namespace string
}

// CollectResources gathers all Fleet diagnostic resources and returns a Snapshot.
func (col *Collector) CollectResources(ctx context.Context, c client.Client) (*Snapshot, error) {
	timestamp := time.Now().UTC().Format(time.RFC3339)

	// Collect controller info
	controllerInfo, err := col.getControllerInfo(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get controller info: %w", err)
	}

	// Collect GitRepos
	gitRepos, err := col.getGitRepos(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get gitrepos: %w", err)
	}

	// Collect Bundles
	bundles, err := col.getBundles(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get bundles: %w", err)
	}

	// Collect BundleDeployments
	bundleDeployments, err := col.getBundleDeployments(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get bundledeployments: %w", err)
	}

	// Collect Contents
	contents, err := col.getContents(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get contents: %w", err)
	}

	// Collect bundle lifecycle secrets
	bundleSecrets := col.getBundleSecrets(ctx, c)

	// Collect orphaned secrets - reuse bundleSecrets to avoid re-fetching
	orphanedSecrets := col.getOrphanedSecrets(bundleSecrets, bundles, bundleDeployments)

	// Check content issues
	contentIssues := col.checkContentIssues(bundleDeployments, contents)

	// Check API consistency
	apiConsistency, err := col.checkAPIConsistency(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to check API consistency: %w", err)
	}

	// Get recent events
	recentEvents, err := col.getRecentEvents(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent events: %w", err)
	}

	// Collect Clusters
	clusters, err := col.getClusters(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get clusters: %w", err)
	}

	// Collect ClusterGroups
	clusterGroups, err := col.getClusterGroups(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get clustergroups: %w", err)
	}

	// Collect diagnostics
	diagnostics := col.collectDiagnostics(gitRepos, bundles, bundleDeployments, contents, clusters, clusterGroups, orphanedSecrets, contentIssues)

	return &Snapshot{
		Timestamp:         timestamp,
		Controller:        controllerInfo,
		GitRepos:          col.convertGitRepos(gitRepos),
		Bundles:           col.convertBundles(bundles),
		BundleDeployments: col.convertBundleDeployments(bundleDeployments),
		Contents:          col.convertContents(contents),
		Clusters:          col.convertClusters(clusters),
		ClusterGroups:     col.convertClusterGroups(clusterGroups),
		BundleSecrets:     col.convertSecrets(bundleSecrets),
		OrphanedSecrets:   col.convertSecrets(orphanedSecrets),
		ContentIssues:     contentIssues,
		APIConsistency:    apiConsistency,
		RecentEvents:      col.convertEvents(recentEvents),
		Diagnostics:       diagnostics,
	}, nil
}

// Conversion functions to extract only relevant fields

func (col *Collector) convertGitRepos(gitRepos []fleet.GitRepo) []GitRepoInfo {
	result := make([]GitRepoInfo, 0, len(gitRepos))
	for _, gr := range gitRepos {
		info := GitRepoInfo{
			Namespace:           gr.Namespace,
			Name:                gr.Name,
			Generation:          gr.Generation,
			ObservedGeneration:  gr.Status.ObservedGeneration,
			Commit:              gr.Status.Commit,
			PollingCommit:       gr.Status.PollingCommit,
			WebhookCommit:       gr.Status.WebhookCommit,
			LastPollingTime:     gr.Status.LastPollingTime.Time,
			ForceSyncGeneration: gr.Spec.ForceSyncGeneration,
		}

		if gr.Spec.PollingInterval == nil || gr.Spec.PollingInterval.Duration == 0 {
			info.PollingInterval = reconciler.GetPollingIntervalDuration(&gr)
		} else {
			info.PollingInterval = gr.Spec.PollingInterval.Duration
		}

		// Extract ready status
		for _, cond := range gr.Status.Conditions {
			if cond.Type == "Ready" {
				info.Ready = cond.Status == "True"
				if cond.Status != "True" {
					info.ReadyMessage = cond.Message
				}
				break
			}
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertBundles(bundles []fleet.Bundle) []BundleInfo {
	result := make([]BundleInfo, 0, len(bundles))
	for _, b := range bundles {
		info := BundleInfo{
			Namespace:           b.Namespace,
			Name:                b.Name,
			UID:                 string(b.UID),
			Generation:          b.Generation,
			ObservedGeneration:  b.Status.ObservedGeneration,
			Commit:              b.Labels[fleet.CommitLabel],
			RepoName:            b.Labels[fleet.RepoLabel],
			Labels:              b.Labels,
			ForceSyncGeneration: b.Spec.ForceSyncGeneration,
			ResourcesSHA256Sum:  b.Status.ResourcesSHA256Sum,
			Finalizers:          b.Finalizers,
		}

		if b.DeletionTimestamp != nil {
			ts := b.DeletionTimestamp.UTC().Format(time.RFC3339)
			info.DeletionTimestamp = &ts
		}

		// Compute bundle size by marshaling the entire Bundle resource to JSON
		// This includes spec.resources (all manifests) and the status
		if bundleJSON, err := json.Marshal(b); err == nil {
			size := int64(len(bundleJSON))
			info.SizeBytes = &size
		}

		// Extract ready status and errors
		for _, cond := range b.Status.Conditions {
			if cond.Type == "Ready" {
				info.Ready = cond.Status == "True"
				if cond.Status != "True" {
					info.ReadyMessage = cond.Message
				}
				break
			}
		}

		// Extract error messages from summary
		if b.Status.Summary.NotReady > 0 && len(b.Status.Summary.NonReadyResources) > 0 {
			info.ErrorMessage = b.Status.Summary.NonReadyResources[0].Message
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertBundleDeployments(bds []fleet.BundleDeployment) []BundleDeploymentInfo {
	result := make([]BundleDeploymentInfo, 0, len(bds))
	for _, bd := range bds {
		info := BundleDeploymentInfo{
			Namespace:           bd.Namespace,
			Name:                bd.Name,
			UID:                 string(bd.UID),
			Generation:          bd.Generation,
			Commit:              bd.Labels[fleet.CommitLabel],
			ForceSyncGeneration: bd.Spec.Options.ForceSyncGeneration,
			SyncGeneration:      bd.Status.SyncGeneration,
			DeploymentID:        bd.Spec.DeploymentID,
			StagedDeploymentID:  bd.Spec.StagedDeploymentID,
			AppliedDeploymentID: bd.Status.AppliedDeploymentID,
			Finalizers:          bd.Finalizers,
			Labels:              bd.Labels,
			BundleName:          bd.Labels[fleet.BundleLabel],
			BundleNamespace:     bd.Labels[fleet.BundleNamespaceLabel],
		}

		if bd.DeletionTimestamp != nil {
			ts := bd.DeletionTimestamp.UTC().Format(time.RFC3339)
			info.DeletionTimestamp = &ts
		}

		// Extract ready status
		for _, cond := range bd.Status.Conditions {
			if cond.Type == "Ready" {
				info.Ready = cond.Status == "True"
				if cond.Status != "True" {
					info.ReadyMessage = cond.Message
				}
				break
			}
		}

		// Extract error messages from nonReadyStatus
		if len(bd.Status.NonReadyStatus) > 0 {
			info.ErrorMessage = bd.Status.NonReadyStatus[0].String()
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertContents(contents []fleet.Content) []ContentInfo {
	result := make([]ContentInfo, 0, len(contents))
	for _, c := range contents {
		info := ContentInfo{
			Name:           c.Name,
			Finalizers:     c.Finalizers,
			ReferenceCount: c.Status.ReferenceCount,
		}

		// Calculate size from content data
		if c.Content != nil {
			info.Size = int64(len(c.Content))
		}

		if c.DeletionTimestamp != nil {
			ts := c.DeletionTimestamp.UTC().Format(time.RFC3339)
			info.DeletionTimestamp = &ts
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertClusters(clusters []fleet.Cluster) []ClusterInfo {
	result := make([]ClusterInfo, 0, len(clusters))
	for _, cluster := range clusters {
		info := ClusterInfo{
			Namespace:              cluster.Namespace,
			Name:                   cluster.Name,
			AgentNamespace:         cluster.Status.Agent.Namespace,
			BundleDeployments:      cluster.Status.Summary.DesiredReady,
			ReadyBundleDeployments: cluster.Status.Summary.Ready,
		}

		if !cluster.Status.Agent.LastSeen.IsZero() {
			ts := cluster.Status.Agent.LastSeen.UTC().Format(time.RFC3339)
			info.AgentLastSeen = &ts
			age := time.Since(cluster.Status.Agent.LastSeen.Time)
			info.AgentLastSeenAge = age.Round(time.Second).String()
		}

		// Extract ready status
		for _, cond := range cluster.Status.Conditions {
			if cond.Type == "Ready" {
				info.Ready = cond.Status == "True"
				if cond.Status != "True" {
					info.ReadyMessage = cond.Message
				}
				break
			}
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertClusterGroups(clusterGroups []fleet.ClusterGroup) []ClusterGroupInfo {
	result := make([]ClusterGroupInfo, 0, len(clusterGroups))
	for _, cg := range clusterGroups {
		info := ClusterGroupInfo{
			Namespace:    cg.Namespace,
			Name:         cg.Name,
			ClusterCount: cg.Status.ClusterCount,
		}

		// Format selector as string if present
		if cg.Spec.Selector != nil && cg.Spec.Selector.MatchLabels != nil {
			var selectors []string
			for k, v := range cg.Spec.Selector.MatchLabels {
				selectors = append(selectors, fmt.Sprintf("%s=%s", k, v))
			}
			info.Selector = strings.Join(selectors, ",")
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertSecrets(secrets []corev1.Secret) []SecretInfo {
	result := make([]SecretInfo, 0, len(secrets))
	for _, s := range secrets {
		info := SecretInfo{
			Namespace:  s.Namespace,
			Name:       s.Name,
			Type:       string(s.Type),
			Commit:     s.Labels[fleet.CommitLabel],
			Finalizers: s.Finalizers,
		}

		if s.DeletionTimestamp != nil {
			ts := s.DeletionTimestamp.UTC().Format(time.RFC3339)
			info.DeletionTimestamp = &ts
		}

		if len(s.OwnerReferences) > 0 {
			owner := s.OwnerReferences[0]
			info.OwnerKind = owner.Kind
			info.OwnerName = owner.Name
			info.OwnerUID = string(owner.UID)
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) convertEvents(events []corev1.Event) []EventInfo {
	result := make([]EventInfo, 0, len(events))
	for _, e := range events {
		info := EventInfo{
			Namespace:    e.Namespace,
			Type:         e.Type,
			Reason:       e.Reason,
			Message:      e.Message,
			InvolvedKind: e.InvolvedObject.Kind,
			InvolvedName: e.InvolvedObject.Name,
			Count:        e.Count,
		}

		if e.LastTimestamp.Time.IsZero() {
			if !e.EventTime.IsZero() {
				info.LastTimestamp = e.EventTime.UTC().Format(time.RFC3339)
			}
		} else {
			info.LastTimestamp = e.LastTimestamp.UTC().Format(time.RFC3339)
		}

		result = append(result, info)
	}
	return result
}

func (col *Collector) getControllerInfo(ctx context.Context, c client.Client) ([]ControllerInfo, error) {
	podList := &corev1.PodList{}

	appValues := []string{"fleet-controller", "gitjob", "helmops"}

	result := []ControllerInfo{}

	for _, v := range appValues {
		err := c.List(ctx, podList, client.InNamespace(col.SystemNamespace), client.MatchingLabels{"app": v})
		if err != nil {
			return nil, err
		}

		if len(podList.Items) == 0 {
			continue
		}

		for _, pod := range podList.Items {
			info := ControllerInfo{
				Name:   pod.Name,
				Status: string(pod.Status.Phase),
			}

			if pod.Status.StartTime != nil {
				info.StartTime = pod.Status.StartTime.UTC().Format(time.RFC3339)
			}

			if len(pod.Status.ContainerStatuses) > 0 {
				info.Restarts = pod.Status.ContainerStatuses[0].RestartCount
			}

			result = append(result, info)
		}
	}

	return result, nil
}

func (col *Collector) getGitRepos(ctx context.Context, c client.Client) ([]fleet.GitRepo, error) {
	gitRepoList := &fleet.GitRepoList{}
	err := c.List(ctx, gitRepoList)
	if err != nil {
		return nil, err
	}
	return gitRepoList.Items, nil
}

func (col *Collector) getBundles(ctx context.Context, c client.Client) ([]fleet.Bundle, error) {
	bundleList := &fleet.BundleList{}
	err := c.List(ctx, bundleList)
	if err != nil {
		return nil, err
	}
	return bundleList.Items, nil
}

func (col *Collector) getBundleDeployments(ctx context.Context, c client.Client) ([]fleet.BundleDeployment, error) {
	bdList := &fleet.BundleDeploymentList{}
	err := c.List(ctx, bdList)
	if err != nil {
		return nil, err
	}
	return bdList.Items, nil
}

func (col *Collector) getContents(ctx context.Context, c client.Client) ([]fleet.Content, error) {
	contentList := &fleet.ContentList{}
	err := c.List(ctx, contentList)
	if err != nil {
		return nil, err
	}
	return contentList.Items, nil
}

func (col *Collector) getClusters(ctx context.Context, c client.Client) ([]fleet.Cluster, error) {
	clusterList := &fleet.ClusterList{}
	err := c.List(ctx, clusterList, client.InNamespace(col.Namespace))
	if err != nil {
		return nil, err
	}
	return clusterList.Items, nil
}

func (col *Collector) getClusterGroups(ctx context.Context, c client.Client) ([]fleet.ClusterGroup, error) {
	clusterGroupList := &fleet.ClusterGroupList{}
	err := c.List(ctx, clusterGroupList, client.InNamespace(col.Namespace))
	if err != nil {
		return nil, err
	}
	return clusterGroupList.Items, nil
}

func (col *Collector) getBundleSecrets(ctx context.Context, c client.Client) []corev1.Secret {
	var allSecrets []corev1.Secret

	// Get bundle-values secrets
	valuesSecrets := &corev1.SecretList{}
	err := c.List(ctx, valuesSecrets, client.MatchingFields{"type": fleet.SecretTypeBundleValues})
	if err == nil {
		allSecrets = append(allSecrets, valuesSecrets.Items...)
	}

	// Get bundle-deployment secrets
	deploymentSecrets := &corev1.SecretList{}
	err = c.List(ctx, deploymentSecrets, client.MatchingFields{"type": fleet.SecretTypeBundleDeploymentOptions})
	if err == nil {
		allSecrets = append(allSecrets, deploymentSecrets.Items...)
	}

	return allSecrets
}

func (col *Collector) getOrphanedSecrets(bundleSecrets []corev1.Secret, bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment) []corev1.Secret {
	// Create UID maps
	bundleUIDs := make(map[string]string)
	for _, bundle := range bundles {
		bundleUIDs[bundle.Namespace+"/"+bundle.Name] = string(bundle.UID)
	}

	bundleDeploymentUIDs := make(map[string]string)
	for _, bd := range bundleDeployments {
		bundleDeploymentUIDs[bd.Namespace+"/"+bd.Name] = string(bd.UID)
	}

	var orphaned []corev1.Secret
	for _, secret := range bundleSecrets {
		if secret.DeletionTimestamp != nil {
			orphaned = append(orphaned, secret)
			continue
		}

		if len(secret.OwnerReferences) == 0 {
			orphaned = append(orphaned, secret)
			continue
		}

		owner := secret.OwnerReferences[0]
		expectedUID := ""

		switch owner.Kind {
		case "Bundle":
			expectedUID = bundleUIDs[secret.Namespace+"/"+owner.Name]
		case "BundleDeployment":
			expectedUID = bundleDeploymentUIDs[secret.Namespace+"/"+owner.Name]
		}

		if expectedUID == "" || expectedUID != string(owner.UID) {
			orphaned = append(orphaned, secret)
		}
	}

	return orphaned
}

// checkContentIssues identifies BundleDeployments with missing or problematic Content resources
func (col *Collector) checkContentIssues(bundleDeployments []fleet.BundleDeployment, contents []fleet.Content) []ContentIssue {
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

		// Check spec.deploymentID content
		if bd.Spec.DeploymentID != "" {
			contentName := extractContentName(bd.Spec.DeploymentID)
			issue.ContentName = contentName

			content, exists := contentMap[contentName]
			issue.ContentExists = exists

			if !exists {
				issue.Issues = append(issue.Issues, "content_not_found")
			} else if content.DeletionTimestamp != nil {
				ts := content.DeletionTimestamp.UTC().Format(time.RFC3339)
				issue.ContentDeletionTimestamp = &ts
				issue.Issues = append(issue.Issues, "content_has_deletion_timestamp")
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
				}
			}
		}

		if len(issue.Issues) > 0 {
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
func (col *Collector) checkAPIConsistency(ctx context.Context, c client.Client) (*APIConsistency, error) {
	// Test by fetching the namespace multiple times and checking if resourceVersion changes unexpectedly
	namespace := &corev1.Namespace{}
	versions := make([]string, APIConsistencyRetries)

	consistent := true

	for i := range APIConsistencyRetries {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		err := c.Get(ctx, client.ObjectKey{Name: col.Namespace}, namespace)
		if err != nil {
			return nil, err
		}
		versions[i] = namespace.ResourceVersion

		if i > 0 && versions[i] != versions[i-1] { //nolint:gosec // G602 false positive: versions is sized to APIConsistencyRetries, loop bounds guarantee valid indices
			consistent = false
		}
	}

	return &APIConsistency{
		Consistent: consistent,
		Versions:   versions,
	}, nil
}

// getRecentEvents retrieves recent Fleet-related Kubernetes events
func (col *Collector) getRecentEvents(ctx context.Context, c client.Client) ([]corev1.Event, error) {
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
	if len(fleetEvents) > RecentEventsCount {
		fleetEvents = fleetEvents[len(fleetEvents)-RecentEventsCount:]
	}

	return fleetEvents, nil
}

// collectDiagnostics gathers all diagnostic checks and returns a comprehensive diagnostics report
func (col *Collector) collectDiagnostics(
	gitRepos []fleet.GitRepo,
	bundles []fleet.Bundle,
	bundleDeployments []fleet.BundleDeployment,
	contents []fleet.Content,
	clusters []fleet.Cluster,
	clusterGroups []fleet.ClusterGroup,
	orphanedSecrets []corev1.Secret,
	contentIssues []ContentIssue,
) *Diagnostics {
	// Extract secrets with invalid owners from orphaned secrets (UID mismatches)
	invalidSecretOwners := col.filterSecretsWithInvalidOwners(orphanedSecrets)

	gitRepoBundleInconsistencies := col.detectGitRepoBundleInconsistencies(gitRepos, bundles)
	resourcesWithMultipleFinalizers := col.detectMultipleFinalizers(gitRepos, bundles, bundleDeployments)

	return &Diagnostics{
		StuckBundleDeployments:                      col.convertBundleDeployments(col.detectStuckBundleDeployments(bundleDeployments)),
		GitRepoBundleInconsistencies:                col.convertBundles(gitRepoBundleInconsistencies),
		InvalidSecretOwners:                         col.convertSecrets(invalidSecretOwners),
		ResourcesWithMultipleFinalizers:             resourcesWithMultipleFinalizers,
		LargeBundles:                                col.convertBundles(col.detectLargeBundles(bundles)),
		BundlesWithMissingContent:                   col.convertBundles(col.detectBundlesWithMissingContent(bundles, contents)),
		BundlesWithNoDeployments:                    col.convertBundles(col.detectBundlesWithNoDeployments(bundles, bundleDeployments)),
		GitReposWithNoBundles:                       col.convertGitRepos(col.detectGitReposWithNoBundles(gitRepos, bundles)),
		ClustersWithAgentIssues:                     col.convertClusters(col.detectClustersWithAgentIssues(clusters)),
		ClusterGroupsWithNoClusters:                 col.convertClusterGroups(col.detectClusterGroupsWithNoClusters(clusterGroups)),
		BundlesWithMissingGitRepo:                   col.convertBundles(col.detectBundlesWithMissingGitRepo(bundles, gitRepos)),
		BundleDeploymentsWithMissingBundle:          col.convertBundleDeployments(col.detectBundleDeploymentsWithMissingBundle(bundleDeployments, bundles)),
		GitReposWithCommitMismatch:                  col.convertGitRepos(col.detectGitReposWithCommitMismatch(gitRepos)),
		GitReposWithGenerationMismatch:              col.convertGitRepos(col.detectGitReposWithGenerationMismatch(gitRepos)),
		GitReposUnpolled:                            col.convertGitRepos(col.detectUnpolledGitRepos(gitRepos)),
		BundlesWithGenerationMismatch:               col.convertBundles(col.detectBundlesWithGenerationMismatch(bundles)),
		BundleDeploymentsWithSyncGenerationMismatch: col.convertBundleDeployments(col.detectBundleDeploymentsWithSyncGenerationMismatch(bundleDeployments)),
		OrphanedSecretsCount:                        len(orphanedSecrets),
		InvalidSecretOwnersCount:                    len(invalidSecretOwners),
		ContentIssuesCount:                          len(contentIssues),
		GitRepoBundleInconsistenciesCount:           len(gitRepoBundleInconsistencies),
		ResourcesWithMultipleFinalizersCount:        len(resourcesWithMultipleFinalizers),
		BundlesWithDeletionTimestamp:                col.countBundlesWithDeletionTimestamp(bundles),
		BundleDeploymentsWithDeletionTimestamp:      col.countBundleDeploymentsWithDeletionTimestamp(bundleDeployments),
		ContentsWithDeletionTimestamp:               col.countContentsWithDeletionTimestamp(contents),
		ContentsWithZeroReferenceCount:              col.countContentsWithZeroReferenceCount(contents),
	}
}

// detectStuckBundleDeployments identifies BundleDeployments stuck due to various issues
func (col *Collector) detectStuckBundleDeployments(bundleDeployments []fleet.BundleDeployment) []fleet.BundleDeployment {
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
func (col *Collector) detectGitRepoBundleInconsistencies(gitRepos []fleet.GitRepo, bundles []fleet.Bundle) []fleet.Bundle {
	var inconsistentBundles []fleet.Bundle
	for _, bundle := range bundles {
		repoName, ok := bundle.Labels[fleet.RepoLabel]
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
		if bundle.Labels[fleet.CommitLabel] != gitRepo.Status.Commit {
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
func (col *Collector) filterSecretsWithInvalidOwners(orphanedSecrets []corev1.Secret) []corev1.Secret {
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
func (col *Collector) countBundlesWithDeletionTimestamp(bundles []fleet.Bundle) int {
	count := 0
	for _, bundle := range bundles {
		if bundle.DeletionTimestamp != nil {
			count++
		}
	}
	return count
}

// countBundleDeploymentsWithDeletionTimestamp counts BundleDeployments with deletion timestamps
func (col *Collector) countBundleDeploymentsWithDeletionTimestamp(bundleDeployments []fleet.BundleDeployment) int {
	count := 0
	for _, bd := range bundleDeployments {
		if bd.DeletionTimestamp != nil {
			count++
		}
	}
	return count
}

// countContentsWithDeletionTimestamp counts contents with deletion timestamps
func (col *Collector) countContentsWithDeletionTimestamp(contents []fleet.Content) int {
	count := 0
	for _, content := range contents {
		if content.DeletionTimestamp != nil {
			count++
		}
	}
	return count
}

// countContentsWithZeroReferenceCount counts non-deleted contents with reference counts set to 0.
func (col *Collector) countContentsWithZeroReferenceCount(contents []fleet.Content) int {
	count := 0
	for _, content := range contents {
		if content.DeletionTimestamp == nil && content.Status.ReferenceCount == 0 {
			count++
		}
	}
	return count
}

// detectMultipleFinalizers identifies resources with more than one finalizer (indicates potential bugs)
func (col *Collector) detectMultipleFinalizers(gitRepos []fleet.GitRepo, bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment) []ResourceWithFinalizers {
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
func (col *Collector) detectLargeBundles(bundles []fleet.Bundle) []fleet.Bundle {
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
func (col *Collector) detectBundlesWithMissingContent(bundles []fleet.Bundle, contents []fleet.Content) []fleet.Bundle {
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
func (col *Collector) detectBundlesWithNoDeployments(bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment) []fleet.Bundle {
	var bundlesWithNoDeployments []fleet.Bundle

	// Count deployments per bundle using bundle-name and bundle-namespace labels
	bundleDeploymentCounts := make(map[string]int)
	for _, bd := range bundleDeployments {
		bundleName := bd.Labels[fleet.BundleLabel]
		bundleNamespace := bd.Labels[fleet.BundleNamespaceLabel]
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
func (col *Collector) detectGitReposWithNoBundles(gitRepos []fleet.GitRepo, bundles []fleet.Bundle) []fleet.GitRepo {
	var gitReposWithNoBundles []fleet.GitRepo

	// Count bundles per gitrepo
	bundleCounts := make(map[string]int)
	for _, bundle := range bundles {
		repoName := bundle.Labels[fleet.RepoLabel]
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
func (col *Collector) detectClustersWithAgentIssues(clusters []fleet.Cluster) []fleet.Cluster {
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
func (col *Collector) detectClusterGroupsWithNoClusters(clusterGroups []fleet.ClusterGroup) []fleet.ClusterGroup {
	var clusterGroupsWithNoClusters []fleet.ClusterGroup

	for _, cg := range clusterGroups {
		if cg.Status.ClusterCount == 0 {
			clusterGroupsWithNoClusters = append(clusterGroupsWithNoClusters, cg)
		}
	}

	return clusterGroupsWithNoClusters
}

// detectBundlesWithMissingGitRepo detects Bundles that reference a missing GitRepo.
// Bundles without the repo-name label (e.g., agent bundles) are excluded.
func (col *Collector) detectBundlesWithMissingGitRepo(bundles []fleet.Bundle, gitRepos []fleet.GitRepo) []fleet.Bundle {
	var bundlesWithMissingGitRepo []fleet.Bundle

	// Create map of gitrepo names
	gitRepoExists := make(map[string]bool)
	for _, gr := range gitRepos {
		key := gr.Namespace + "/" + gr.Name
		gitRepoExists[key] = true
	}

	for _, bundle := range bundles {
		repoName := bundle.Labels[fleet.RepoLabel]
		// Skip bundles without repo-name label (e.g., agent bundles)
		if repoName == "" {
			continue
		}
		key := bundle.Namespace + "/" + repoName
		if !gitRepoExists[key] {
			bundlesWithMissingGitRepo = append(bundlesWithMissingGitRepo, bundle)
		}
	}

	return bundlesWithMissingGitRepo
}

// detectBundleDeploymentsWithMissingBundle detects orphaned BundleDeployments
func (col *Collector) detectBundleDeploymentsWithMissingBundle(bundleDeployments []fleet.BundleDeployment, bundles []fleet.Bundle) []fleet.BundleDeployment {
	var orphanedBundleDeployments []fleet.BundleDeployment

	// Create map of bundle names
	bundleExists := make(map[string]bool)
	for _, bundle := range bundles {
		key := bundle.Namespace + "/" + bundle.Name
		bundleExists[key] = true
	}

	for _, bd := range bundleDeployments {
		bundleName := bd.Labels[fleet.BundleLabel]
		bundleNamespace := bd.Labels[fleet.BundleNamespaceLabel]
		if bundleName != "" && bundleNamespace != "" {
			key := bundleNamespace + "/" + bundleName
			if !bundleExists[key] {
				orphanedBundleDeployments = append(orphanedBundleDeployments, bd)
			}
		}
	}

	return orphanedBundleDeployments
}

// detectGitReposWithCommitMismatch detects GitRepos with their status commit not matching any of the same status'
// webhook or polling commits.
func (col *Collector) detectGitReposWithCommitMismatch(gitRepos []fleet.GitRepo) []fleet.GitRepo {
	var mismatchedGitRepos []fleet.GitRepo

	for _, gr := range gitRepos {
		if gr.Status.Commit != gr.Status.PollingCommit && gr.Status.Commit != gr.Status.WebhookCommit {
			mismatchedGitRepos = append(mismatchedGitRepos, gr)
		}
	}

	return mismatchedGitRepos
}

// detectUnpolledGitRepos detects GitRepos which last poll happened too long ago considering their polling intervals.
// It uses default polling interval settings set in Fleet controllers if no interval is set.
func (col *Collector) detectUnpolledGitRepos(gitRepos []fleet.GitRepo) []fleet.GitRepo {
	var unpolledGitRepos []fleet.GitRepo

	for _, gr := range gitRepos {
		var interval time.Duration

		if gr.Spec.PollingInterval == nil || gr.Spec.PollingInterval.Duration == 0 {
			interval = reconciler.GetPollingIntervalDuration(&gr)
		} else {
			interval = gr.Spec.PollingInterval.Duration
		}

		if gr.Status.LastPollingTime.Time.Before(time.Now().Add(-1 * interval)) {
			unpolledGitRepos = append(unpolledGitRepos, gr)
		}
	}

	return unpolledGitRepos
}

// detectGitReposWithGenerationMismatch detects GitRepos with generation != observedGeneration
func (col *Collector) detectGitReposWithGenerationMismatch(gitRepos []fleet.GitRepo) []fleet.GitRepo {
	var mismatchedGitRepos []fleet.GitRepo

	for _, gr := range gitRepos {
		if gr.Generation != gr.Status.ObservedGeneration {
			mismatchedGitRepos = append(mismatchedGitRepos, gr)
		}
	}

	return mismatchedGitRepos
}

// detectBundlesWithGenerationMismatch detects Bundles with generation != observedGeneration
func (col *Collector) detectBundlesWithGenerationMismatch(bundles []fleet.Bundle) []fleet.Bundle {
	var mismatchedBundles []fleet.Bundle

	for _, bundle := range bundles {
		if bundle.Generation != bundle.Status.ObservedGeneration {
			mismatchedBundles = append(mismatchedBundles, bundle)
		}
	}

	return mismatchedBundles
}

// detectBundleDeploymentsWithSyncGenerationMismatch detects BundleDeployments with syncGeneration != forceSyncGeneration
func (col *Collector) detectBundleDeploymentsWithSyncGenerationMismatch(bundleDeployments []fleet.BundleDeployment) []fleet.BundleDeployment {
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
