package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/rancher/fleet/pkg/troubleshooting"

	command "github.com/rancher/fleet/internal/cmd"
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
	col := &troubleshooting.Collector{
		SystemNamespace: m.System,
		Namespace:       m.Namespace,
	}
	resources, err := col.CollectResources(ctx, c)
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
