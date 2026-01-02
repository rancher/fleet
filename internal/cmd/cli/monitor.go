package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// Monitor provides diagnostic monitoring of Fleet resources to identify issues with bundle deployments.
//
// # Overview
//
// The monitor command collects and analyzes Fleet resources to diagnose why bundles get stuck during
// the targeting and deployment phases. It outputs compact JSON snapshots containing only the diagnostic-relevant
// fields from Fleet resources, making it easy to identify issues without the verbosity of full Kubernetes
// resource definitions. Use jq or similar tools to format and query the output.
//
// # What It Detects
//
// The monitor command checks for all critical issues that can cause bundles to get stuck:
//
//   - API Time Travel: Detects when the Kubernetes API server returns stale cached resource versions
//   - Old forceSyncGeneration: Identifies bundles that haven't updated to match their GitRepo's forceSyncGeneration
//   - UID Mismatches: Finds secrets with invalid owner references (owner was deleted and recreated)
//   - Deletion Timestamps: Tracks resources stuck with deletion timestamps due to blocking finalizers
//   - Stale Commit Hashes: Detects bundles that haven't updated to their GitRepo's latest commit
//   - DeploymentID Mismatches: Identifies BundleDeployments where spec.deploymentID != status.appliedDeploymentID
//   - Missing Content: Detects when referenced Content resources are missing or have deletion timestamps
//   - Controller Restarts: Tracks Fleet controller pod restarts which can indicate cache issues
//   - Multiple Finalizers: Identifies GitRepos, Bundles, and BundleDeployments with more than one finalizer (indicates a bug; only Contents use multiple finalizers for ref counting)
//   - Large Bundles: Bundles with content >1MB that may cause etcd performance issues
//   - Missing Content Resources: Bundles with resourcesSHA256Sum but no corresponding Content resource
//   - Target Matching Issues: Bundles with zero BundleDeployments (no clusters matched), GitRepos with zero Bundles, ClusterGroups with zero clusters
//   - Agent Connectivity: Clusters with non-ready agents, missing lastSeen timestamps, or stale lastSeen (>24h)
//   - Broken Ownership Chains: Bundles without GitRepos, BundleDeployments without Bundles
//   - Generation Mismatches: GitRepos and Bundles where generation != observedGeneration (reconciliation not progressing)
//
// # When to Use
//
// Use the monitor command when:
//
//   - Bundles are stuck and not deploying to target clusters
//   - GitRepo shows Ready but bundles aren't updating
//   - BundleDeployments are not applying new deploymentIDs
//   - Need to capture the state of Fleet resources for troubleshooting
//   - Debugging issues before/after making changes to GitRepos or bundles
//   - Investigating why resources have old commits or forceSyncGeneration values
//   - Checking if bundles are matching target clusters correctly
//   - Diagnosing agent connectivity issues
//   - Investigating etcd performance problems with large bundles
//
// # Output Format
//
// The command outputs compact JSON (use jq to format) with the following structure:
//
//   - timestamp: When the snapshot was taken (RFC3339)
//   - controller: Fleet controller pod status (name, restarts, uptime)
//   - gitrepos: Array of GitRepo info (generation, observedGeneration, commit, forceSyncGeneration, ready status)
//   - bundles: Array of Bundle info (UID, generation, observedGeneration, commit, resourcesSHA256Sum, finalizers, ready status)
//   - bundledeployments: Array of BundleDeployment info (UID, generation, forceSyncGeneration, syncGeneration, deploymentIDs, ready status)
//   - contents: Array of Content info (name, size, finalizers, deletion timestamps)
//   - clusters: Array of Cluster info (agent status, lastSeen, age, ready status, bundledeployment counts)
//   - clustergroups: Array of ClusterGroup info (cluster counts, selector)
//   - bundleSecrets: Array of bundle lifecycle secrets (commit, owners, finalizers)
//   - orphanedSecrets: Secrets with invalid owner references or deletion timestamps
//   - contentIssues: BundleDeployments with missing or problematic Content resources
//   - apiConsistency: Results of API server consistency check (detects time travel)
//   - recentEvents: Last 20 Fleet-related Kubernetes events
//   - diagnostics: Summary of all detected issues with arrays of affected resources
//
// # Usage Examples
//
// Basic usage - single snapshot:
//
//	fleet monitor | jq
//
// Watch mode - continuous monitoring every 5 seconds:
//
//	fleet monitor --watch --interval 5
//
// Monitor a specific namespace:
//
//	fleet monitor -n fleet-local | jq
//
// Analyze the output with jq:
//
//	# Show summary with formatting
//	fleet monitor | jq '{timestamp, controller, diagnostics}'
//
//	# Check bundles with generation mismatch
//	fleet monitor | jq '.diagnostics.bundlesWithGenerationMismatch'
//
//	# Find bundles with old commits
//	fleet monitor | jq '.diagnostics.gitrepoBundleInconsistencies'
//
//	# Check for large bundles
//	fleet monitor | jq '.diagnostics.largeBundles'
//
//	# Check agent connectivity issues
//	fleet monitor | jq '.diagnostics.clustersWithAgentIssues'
//
//	# Check target matching problems
//	fleet monitor | jq '{
//	  bundlesWithNoDeployments: (.diagnostics.bundlesWithNoDeployments | length),
//	  gitreposWithNoBundles: (.diagnostics.gitreposWithNoBundles | length),
//	  clustergroupsWithNoClusters: (.diagnostics.clustergroupsWithNoClusters | length)
//	}'
//
//	# Check generation mismatches
//	fleet monitor | jq '{
//	  gitrepos: .diagnostics.gitreposWithGenerationMismatch,
//	  bundles: .diagnostics.bundlesWithGenerationMismatch
//	}'
//
//	# Monitor agent health over time
//	while true; do
//	  fleet monitor | jq '.clusters[] | {name, agentLastSeenAge, ready}'
//	  sleep 30
//	done
//
//	# Check API consistency
//	fleet monitor | jq '.apiConsistency'
//
// Compare before/after snapshots:
//
//	# Before change
//	fleet monitor > before.json
//
//	# Make your change (e.g., update GitRepo)
//	kubectl edit gitrepo/my-repo -n fleet-local
//
//	# After change
//	fleet monitor > after.json
//
//	# Compare commits
//	diff <(jq '.bundles[] | {name, commit}' before.json) \
//	     <(jq '.bundles[] | {name, commit}' after.json)
//
// # Troubleshooting Workflow
//
// 1. Capture initial state:
//
//	fleet monitor > initial.json
//
// 2. Check diagnostics for issues:
//
//	cat initial.json | jq '.diagnostics'
//
// 3. If bundles are stuck, check specific issues:
//
//	# Check for generation mismatches
//	cat initial.json | jq '.diagnostics.bundlesWithGenerationMismatch[] | {name, generation, observedGeneration}'
//
//	# Check for deployment ID mismatches
//	cat initial.json | jq '.diagnostics.stuckBundleDeployments[] | {name, deploymentID, appliedDeploymentID}'
//
//	# Check for orphaned secrets
//	cat initial.json | jq '.orphanedSecrets[] | {name, ownerUID}'
//
// 4. Fix the identified issues and capture new state:
//
//	fleet monitor > fixed.json
//
// 5. Verify the fix by comparing:
//
//	diff <(jq -S . initial.json) <(jq -S . fixed.json)
//
// # Related Commands
//
// For live cluster monitoring, also consider:
//   - kubectl get bundles -A -w: Watch bundle resources
//   - kubectl get bundledeployments -A -w: Watch bundledeployment resources
//   - kubectl describe bundle <name>: Detailed bundle information
//   - kubectl logs -n cattle-fleet-system deploy/fleet-controller: Controller logs
type Monitor struct {
	FleetClient
	Watch    bool   `usage:"Watch for changes and output continuously"`
	Interval int    `usage:"Interval in seconds between checks when watching (default: 60)" default:"60"`
	System   string `usage:"Fleet system namespace (default: cattle-fleet-system)" default:"cattle-fleet-system"`
}

func NewMonitor() *cobra.Command {
	cmd := command.Command(&Monitor{}, cobra.Command{
		Use:   "monitor",
		Short: "Monitor Fleet resources and diagnose bundle deployment issues",
		Long: `Monitor Fleet resources and diagnose bundle deployment issues.

This command collects diagnostic information about Fleet resources including GitRepos,
Bundles, BundleDeployments, and related resources. It outputs JSON containing only the
fields relevant for troubleshooting, making it easy to identify issues like:

  • Bundles stuck with old commits or forceSyncGeneration
  • BundleDeployments not applying their target deploymentID
  • Orphaned secrets with invalid owner references
  • Resources stuck with deletion timestamps due to finalizers
  • API server consistency issues (time travel)
  • Missing or problematic Content resources

The output is designed to be piped to jq for analysis or saved for comparison over time.

Examples:
  # Single snapshot
  fleet monitor > snapshot.json

  # Continuous monitoring
  fleet monitor --watch --interval 5

  # Check diagnostics summary
  fleet monitor | jq '.diagnostics'

  # Find bundles with generation mismatch
  fleet monitor | jq '.diagnostics.bundlesWithGenerationMismatch'`,
	})
	cmd.SetOut(os.Stdout)

	// add command line flags from zap and controller-runtime, which use
	// goflags and convert them to pflags
	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	ctrl.RegisterFlags(fs)
	cmd.Flags().AddGoFlagSet(fs)
	return cmd
}

type Snapshot struct {
	// Timestamp is an UTC timestamp of when the snapshot was created.
	Timestamp         string                 `json:"timestamp"`
	Controller        []ControllerInfo       `json:"controller,omitempty"`
	GitRepos          []GitRepoInfo          `json:"gitrepos,omitempty"`
	Bundles           []BundleInfo           `json:"bundles,omitempty"`
	BundleDeployments []BundleDeploymentInfo `json:"bundledeployments,omitempty"`
	Contents          []ContentInfo          `json:"contents,omitempty"`
	Clusters          []ClusterInfo          `json:"clusters,omitempty"`
	ClusterGroups     []ClusterGroupInfo     `json:"clustergroups,omitempty"`
	BundleSecrets     []SecretInfo           `json:"bundleSecrets,omitempty"`
	OrphanedSecrets   []SecretInfo           `json:"orphanedSecrets,omitempty"`
	ContentIssues     []ContentIssue         `json:"contentIssues,omitempty"`
	APIConsistency    *APIConsistency        `json:"apiConsistency,omitempty"`
	RecentEvents      []EventInfo            `json:"recentEvents,omitempty"`
	Diagnostics       *Diagnostics           `json:"diagnostics,omitempty"`
}

type ControllerInfo struct {
	Name      string `json:"name"`
	Restarts  int32  `json:"restarts"`
	Status    string `json:"status"`
	StartTime string `json:"startTime,omitempty"`
}

type GitRepoInfo struct {
	Namespace           string        `json:"namespace"`
	Name                string        `json:"name"`
	Generation          int64         `json:"generation"`
	ObservedGeneration  int64         `json:"observedGeneration,omitempty"`
	Commit              string        `json:"commit,omitempty"`
	PollingCommit       string        `json:"pollingCommit,omitempty"`
	WebhookCommit       string        `json:"webhookCommit,omitempty"`
	LastPollingTime     time.Time     `json:"lastPollingTime,omitempty"`
	PollingInterval     time.Duration `json:"pollingInterval,omitempty"`
	ForceSyncGeneration int64         `json:"forceSyncGeneration,omitempty"`
	Ready               bool          `json:"ready"`
	ReadyMessage        string        `json:"readyMessage,omitempty"`
}

type BundleInfo struct {
	Namespace           string            `json:"namespace"`
	Name                string            `json:"name"`
	UID                 string            `json:"uid"`
	Generation          int64             `json:"generation"`
	ObservedGeneration  int64             `json:"observedGeneration,omitempty"`
	Commit              string            `json:"commit,omitempty"`
	RepoName            string            `json:"repoName,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	ForceSyncGeneration int64             `json:"forceSyncGeneration,omitempty"`
	ResourcesSHA256Sum  string            `json:"resourcesSHA256Sum,omitempty"`
	SizeBytes           *int64            `json:"sizeBytes,omitempty"`
	DeletionTimestamp   *string           `json:"deletionTimestamp,omitempty"`
	Finalizers          []string          `json:"finalizers,omitempty"`
	Ready               bool              `json:"ready"`
	ReadyMessage        string            `json:"readyMessage,omitempty"`
	ErrorMessage        string            `json:"errorMessage,omitempty"`
}

// Note: syncGeneration tracks forceSyncGeneration application, NOT resource generation.
// A BundleDeployment is stuck if:
//  1. forceSyncGeneration > 0 and syncGeneration != forceSyncGeneration (forced sync not applied)
//  2. deploymentID != appliedDeploymentID (new content not applied)
//  3. deletionTimestamp is set (being deleted but finalizers blocking)
type BundleDeploymentInfo struct {
	Namespace           string            `json:"namespace"`
	Name                string            `json:"name"`
	UID                 string            `json:"uid"`
	Generation          int64             `json:"generation"`
	Commit              string            `json:"commit,omitempty"`
	ForceSyncGeneration int64             `json:"forceSyncGeneration,omitempty"`
	SyncGeneration      *int64            `json:"syncGeneration,omitempty"`
	DeploymentID        string            `json:"deploymentID,omitempty"`
	StagedDeploymentID  string            `json:"stagedDeploymentID,omitempty"`
	AppliedDeploymentID string            `json:"appliedDeploymentID,omitempty"`
	DeletionTimestamp   *string           `json:"deletionTimestamp,omitempty"`
	Finalizers          []string          `json:"finalizers,omitempty"`
	Ready               bool              `json:"ready"`
	ReadyMessage        string            `json:"readyMessage,omitempty"`
	ErrorMessage        string            `json:"errorMessage,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	BundleName          string            `json:"bundleName,omitempty"`
	BundleNamespace     string            `json:"bundleNamespace,omitempty"`
}

type ContentInfo struct {
	Name              string   `json:"name"`
	Size              int64    `json:"size,omitempty"`
	DeletionTimestamp *string  `json:"deletionTimestamp,omitempty"`
	Finalizers        []string `json:"finalizers,omitempty"`
	ReferenceCount    int      `json:"referenceCount,omitempty"`
}

type SecretInfo struct {
	Namespace         string   `json:"namespace"`
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	Commit            string   `json:"commit,omitempty"`
	DeletionTimestamp *string  `json:"deletionTimestamp,omitempty"`
	Finalizers        []string `json:"finalizers,omitempty"`
	OwnerKind         string   `json:"ownerKind,omitempty"`
	OwnerName         string   `json:"ownerName,omitempty"`
	OwnerUID          string   `json:"ownerUID,omitempty"`
}

type EventInfo struct {
	Namespace     string `json:"namespace"`
	Type          string `json:"type"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	InvolvedKind  string `json:"involvedKind"`
	InvolvedName  string `json:"involvedName"`
	Count         int32  `json:"count"`
	LastTimestamp string `json:"lastTimestamp,omitempty"`
}

type ContentIssue struct {
	Namespace                string   `json:"namespace"`
	Name                     string   `json:"name"`
	ContentName              string   `json:"contentName,omitempty"`
	StagedContentName        string   `json:"stagedContentName,omitempty"`
	AppliedContentName       string   `json:"appliedContentName,omitempty"`
	ContentExists            bool     `json:"contentExists,omitempty"`
	ContentDeletionTimestamp *string  `json:"contentDeletionTimestamp,omitempty"`
	StagedContentExists      *bool    `json:"stagedContentExists,omitempty"`
	AppliedContentExists     *bool    `json:"appliedContentExists,omitempty"`
	Issues                   []string `json:"issues"`
}

type APIConsistency struct {
	Consistent bool     `json:"consistent"`
	Versions   []string `json:"versions"`
}

type Diagnostics struct {
	StuckBundleDeployments                      []BundleDeploymentInfo   `json:"stuckBundleDeployments,omitempty"`
	GitRepoBundleInconsistencies                []BundleInfo             `json:"gitRepoBundleInconsistencies,omitempty"`
	InvalidSecretOwners                         []SecretInfo             `json:"invalidSecretOwners,omitempty"`
	ResourcesWithMultipleFinalizers             []ResourceWithFinalizers `json:"resourcesWithMultipleFinalizers,omitempty"`
	LargeBundles                                []BundleInfo             `json:"largeBundles,omitempty"`
	BundlesWithMissingContent                   []BundleInfo             `json:"bundlesWithMissingContent,omitempty"`
	BundlesWithNoDeployments                    []BundleInfo             `json:"bundlesWithNoDeployments,omitempty"`
	GitReposWithNoBundles                       []GitRepoInfo            `json:"gitReposWithNoBundles,omitempty"`
	ClustersWithAgentIssues                     []ClusterInfo            `json:"clustersWithAgentIssues,omitempty"`
	ClusterGroupsWithNoClusters                 []ClusterGroupInfo       `json:"clusterGroupsWithNoClusters,omitempty"`
	BundlesWithMissingGitRepo                   []BundleInfo             `json:"bundlesWithMissingGitRepo,omitempty"`
	BundleDeploymentsWithMissingBundle          []BundleDeploymentInfo   `json:"bundleDeploymentsWithMissingBundle,omitempty"`
	GitReposWithCommitMismatch                  []GitRepoInfo            `json:"gitReposWithCommitMismatch,omitempty"`
	GitReposWithGenerationMismatch              []GitRepoInfo            `json:"gitReposWithGenerationMismatch,omitempty"`
	GitReposUnpolled                            []GitRepoInfo            `json:"gitReposUnpolled,omitempty"`
	BundlesWithGenerationMismatch               []BundleInfo             `json:"bundlesWithGenerationMismatch,omitempty"`
	BundleDeploymentsWithSyncGenerationMismatch []BundleDeploymentInfo   `json:"bundleDeploymentsWithSyncGenerationMismatch,omitempty"`
	OrphanedSecretsCount                        int                      `json:"orphanedSecretsCount,omitempty"`
	InvalidSecretOwnersCount                    int                      `json:"invalidSecretOwnersCount,omitempty"`
	ContentIssuesCount                          int                      `json:"contentIssuesCount,omitempty"`
	GitRepoBundleInconsistenciesCount           int                      `json:"gitRepoBundleInconsistenciesCount,omitempty"`
	ResourcesWithMultipleFinalizersCount        int                      `json:"resourcesWithMultipleFinalizersCount,omitempty"`
	BundlesWithDeletionTimestamp                int                      `json:"bundlesWithDeletionTimestamp,omitempty"`
	BundleDeploymentsWithDeletionTimestamp      int                      `json:"bundleDeploymentsWithDeletionTimestamp,omitempty"`
	ContentsWithDeletionTimestamp               int                      `json:"contentsWithDeletionTimestamp,omitempty"`
	ContentsWithZeroReferenceCount              int                      `json:"contentsWithZeroReferenceCount,omitempty"`
}

type ResourceWithFinalizers struct {
	Kind              string   `json:"kind"`
	Namespace         string   `json:"namespace"`
	Name              string   `json:"name"`
	Finalizers        []string `json:"finalizers"`
	FinalizerCount    int      `json:"finalizerCount"`
	DeletionTimestamp *string  `json:"deletionTimestamp,omitempty"`
}

type ClusterInfo struct {
	Namespace              string  `json:"namespace"`
	Name                   string  `json:"name"`
	AgentNamespace         string  `json:"agentNamespace,omitempty"`
	AgentLastSeen          *string `json:"agentLastSeen,omitempty"`
	AgentLastSeenAge       string  `json:"agentLastSeenAge,omitempty"`
	Ready                  bool    `json:"ready"`
	ReadyMessage           string  `json:"readyMessage,omitempty"`
	BundleDeployments      int     `json:"bundleDeployments,omitempty"`
	ReadyBundleDeployments int     `json:"readyBundleDeployments,omitempty"`
}

type ClusterGroupInfo struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	ClusterCount int    `json:"clusterCount"`
	Selector     string `json:"selector,omitempty"`
}

func (m *Monitor) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	cfg := ctrl.GetConfigOrDie()
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	if m.Watch {
		for {
			if err := m.collectAndOutput(ctx, c, cmd); err != nil {
				return err
			}
			time.Sleep(time.Duration(m.Interval) * time.Second)
		}
	}

	return m.collectAndOutput(ctx, c, cmd)
}

func (m *Monitor) collectAndOutput(ctx context.Context, c client.Client, cmd *cobra.Command) error {
	resources, err := m.collectResources(ctx, c)
	if err != nil {
		return err
	}

	// Output compact JSON - use jq for formatting
	output, err := json.Marshal(resources)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), string(output))
	return nil
}

func (m *Monitor) collectResources(ctx context.Context, c client.Client) (*Snapshot, error) {
	timestamp := time.Now().UTC().Format(time.RFC3339)

	// Collect controller info
	controllerInfo, err := m.getControllerInfo(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get controller info: %w", err)
	}

	// Collect GitRepos
	gitRepos, err := m.getGitRepos(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get gitrepos: %w", err)
	}

	// Collect Bundles
	bundles, err := m.getBundles(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get bundles: %w", err)
	}

	// Collect BundleDeployments
	bundleDeployments, err := m.getBundleDeployments(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get bundledeployments: %w", err)
	}

	// Collect Contents
	contents, err := m.getContents(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get contents: %w", err)
	}

	// Collect bundle lifecycle secrets
	bundleSecrets := m.getBundleSecrets(ctx, c)

	// Collect orphaned secrets - reuse bundleSecrets to avoid re-fetching
	orphanedSecrets := m.getOrphanedSecrets(bundleSecrets, bundles, bundleDeployments)

	// Check content issues
	contentIssues := m.checkContentIssues(bundleDeployments, contents)

	// Check API consistency
	apiConsistency, err := m.checkAPIConsistency(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to check API consistency: %w", err)
	}

	// Get recent events
	recentEvents, err := m.getRecentEvents(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent events: %w", err)
	}

	// Collect Clusters
	clusters, err := m.getClusters(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get clusters: %w", err)
	}

	// Collect ClusterGroups
	clusterGroups, err := m.getClusterGroups(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get clustergroups: %w", err)
	}

	// Collect diagnostics
	diagnostics := m.collectDiagnostics(gitRepos, bundles, bundleDeployments, contents, clusters, clusterGroups, orphanedSecrets, contentIssues)

	return &Snapshot{
		Timestamp:         timestamp,
		Controller:        controllerInfo,
		GitRepos:          m.convertGitRepos(gitRepos),
		Bundles:           m.convertBundles(bundles),
		BundleDeployments: m.convertBundleDeployments(bundleDeployments),
		Contents:          m.convertContents(contents),
		Clusters:          m.convertClusters(clusters),
		ClusterGroups:     m.convertClusterGroups(clusterGroups),
		BundleSecrets:     m.convertSecrets(bundleSecrets),
		OrphanedSecrets:   m.convertSecrets(orphanedSecrets),
		ContentIssues:     contentIssues,
		APIConsistency:    apiConsistency,
		RecentEvents:      m.convertEvents(recentEvents),
		Diagnostics:       diagnostics,
	}, nil
}

// Conversion functions to extract only relevant fields

func (m *Monitor) convertGitRepos(gitRepos []fleet.GitRepo) []GitRepoInfo {
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

func (m *Monitor) convertBundles(bundles []fleet.Bundle) []BundleInfo {
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

func (m *Monitor) convertBundleDeployments(bds []fleet.BundleDeployment) []BundleDeploymentInfo {
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

func (m *Monitor) convertContents(contents []fleet.Content) []ContentInfo {
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

func (m *Monitor) convertClusters(clusters []fleet.Cluster) []ClusterInfo {
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

func (m *Monitor) convertClusterGroups(clusterGroups []fleet.ClusterGroup) []ClusterGroupInfo {
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

func (m *Monitor) convertSecrets(secrets []corev1.Secret) []SecretInfo {
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

func (m *Monitor) convertEvents(events []corev1.Event) []EventInfo {
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

func (m *Monitor) getControllerInfo(ctx context.Context, c client.Client) ([]ControllerInfo, error) {
	podList := &corev1.PodList{}

	appValues := []string{"fleet-controller", "gitjob", "helmops"}

	result := []ControllerInfo{}

	for _, v := range appValues {
		err := c.List(ctx, podList, client.InNamespace(m.System), client.MatchingLabels{"app": v})
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

func (m *Monitor) getGitRepos(ctx context.Context, c client.Client) ([]fleet.GitRepo, error) {
	gitRepoList := &fleet.GitRepoList{}
	err := c.List(ctx, gitRepoList)
	if err != nil {
		return nil, err
	}
	return gitRepoList.Items, nil
}

func (m *Monitor) getBundles(ctx context.Context, c client.Client) ([]fleet.Bundle, error) {
	bundleList := &fleet.BundleList{}
	err := c.List(ctx, bundleList)
	if err != nil {
		return nil, err
	}
	return bundleList.Items, nil
}

func (m *Monitor) getBundleDeployments(ctx context.Context, c client.Client) ([]fleet.BundleDeployment, error) {
	bdList := &fleet.BundleDeploymentList{}
	err := c.List(ctx, bdList)
	if err != nil {
		return nil, err
	}
	return bdList.Items, nil
}

func (m *Monitor) getContents(ctx context.Context, c client.Client) ([]fleet.Content, error) {
	contentList := &fleet.ContentList{}
	err := c.List(ctx, contentList)
	if err != nil {
		return nil, err
	}
	return contentList.Items, nil
}

func (m *Monitor) getClusters(ctx context.Context, c client.Client) ([]fleet.Cluster, error) {
	clusterList := &fleet.ClusterList{}
	err := c.List(ctx, clusterList, client.InNamespace(m.Namespace))
	if err != nil {
		return nil, err
	}
	return clusterList.Items, nil
}

func (m *Monitor) getClusterGroups(ctx context.Context, c client.Client) ([]fleet.ClusterGroup, error) {
	clusterGroupList := &fleet.ClusterGroupList{}
	err := c.List(ctx, clusterGroupList, client.InNamespace(m.Namespace))
	if err != nil {
		return nil, err
	}
	return clusterGroupList.Items, nil
}

func (m *Monitor) getBundleSecrets(ctx context.Context, c client.Client) []corev1.Secret {
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

func (m *Monitor) getOrphanedSecrets(bundleSecrets []corev1.Secret, bundles []fleet.Bundle, bundleDeployments []fleet.BundleDeployment) []corev1.Secret {
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
