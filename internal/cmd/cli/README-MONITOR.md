# Fleet Monitor Command

Advanced diagnostic tool for troubleshooting Fleet GitOps bundle deployments.

## Overview

The `fleet monitor` command provides deep insights into Fleet's GitOps lifecycle by capturing snapshots of all relevant resources and performing automated diagnostics. It helps identify why bundles get stuck during targeting and deployment phases, and provides actionable information about the health of your Fleet installation.

## Quick Start

```bash
# Single snapshot with formatted output
fleet monitor | jq

# Single snapshot with human-readable analysis
fleet monitor | fleet analyze

# Continuous monitoring with built-in watch mode (every 60 seconds)
fleet monitor --watch --interval 60 >> monitor.json

# Analyze collected snapshots (now built into fleet!)
fleet analyze monitor.json

# Show changes between snapshots
fleet analyze --diff monitor.json

# Show only issues
fleet analyze --issues monitor.json
```

## What It Detects

The monitor command performs comprehensive diagnostics to detect:

### Resource Lifecycle Issues
- **Bundles with Generation Mismatch**: Bundles not progressing through their lifecycle (generation != observedGeneration)
- **Stuck BundleDeployments**: BundleDeployments where the agent isn't applying new deploymentIDs
- **Multiple Finalizers**: Resources with more than one finalizer (indicates bugs - only Contents should have multiple
finalizers for ref counting, in Fleet v0.11.1 to v0.14.x)
- **Orphaned Resources**: Resources with deletion timestamps that can't be garbage collected

### Data Consistency Problems
- **API Time Travel**: Kubernetes API server returning stale cached data (detected by fetching resources multiple times)
- **Commit Hash Mismatches**: Bundles/BundleDeployments not updated to GitRepo's latest commit
- **ForceSyncGeneration Drift**: Bundles not reflecting their GitRepo's forceSyncGeneration value
- **UID Mismatches**: Secrets with owner references to deleted/recreated resources
- **DeploymentID Mismatches**: BundleDeployments where spec.deploymentID != status.appliedDeploymentID

### Target Matching Issues
- **Bundles with No Deployments**: Bundles created but no clusters matched the target selector
- **GitRepos with No Bundles**: GitRepos that haven't created any bundles (could be bad path, targets, or processing errors)
- **ClusterGroups with No Clusters**: ClusterGroups with selectors that match no clusters
- **Orphaned BundleDeployments**: BundleDeployments whose parent Bundle was deleted

### Performance Issues
- **Large Bundles**: Bundles >1MB that can impact etcd performance
- **Missing Content Resources**: Bundles with `resourcesSHA256Sum` but no corresponding Content resource
- **High Resource Counts**: Large numbers of bundle resources that may cause etcd pressure

### Agent & Cluster Issues
- **Agent Not Ready**: Clusters with non-ready agent status
- **Missing LastSeen**: Clusters without agent heartbeat timestamp
- **Stale LastSeen**: Clusters where agent hasn't checked in recently (default: 24h, configurable with `--agent-staleness`)
- **Missing Agent Bundles**: Cluster namespaces without expected agent bundle deployments

### Ownership Chain Issues
- **Broken Ownership**: Bundles without GitRepo owners, BundleDeployments without Bundle owners
- **Invalid Secret Owners**: Bundle secrets with incorrect or missing owner references

### Generation/Observation Mismatches
- **GitRepo Generation Drift**: GitRepo generation != observedGeneration (controller not processing updates)
- **Bundle Generation Drift**: Bundle generation != observedGeneration (controller not processing updates)
- **BundleDeployment SyncGeneration Drift**: BundleDeployment syncGeneration != forceSyncGeneration (agent hasn't applied forced sync)
- **Content Stale Generation**: Content resources with outdated generation values

## Output Format

The monitor command outputs **compact JSON** (one snapshot per line). Use `jq` to format for readability:

```bash
fleet monitor | jq
```

### JSON Structure

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "controller": {
    "name": "fleet-controller-abc123",
    "status": "Running",
    "restarts": 0,
    "startTime": "2024-01-15T08:00:00Z"
  },
  "gitrepos": [
    {
      "name": "my-repo",
      "namespace": "fleet-default",
      "generation": 5,
      "observedGeneration": 5,
      "commit": "abc1234567890def",
      "forceSyncGeneration": 2,
      "readyCondition": {
        "status": "True",
        "message": ""
      }
    }
  ],
  "bundles": [
    {
      "name": "my-repo-bundle",
      "namespace": "fleet-default",
      "uid": "abc-123-def",
      "generation": 3,
      "observedGeneration": 3,
      "commit": "abc1234567890def",
      "forceSyncGeneration": 2,
      "resourcesSHA256Sum": "xyz789...",
      "sizeBytes": 524288,
      "finalizers": ["fleet.cattle.io/bundle-cleanup"],
      "deletionTimestamp": null,
      "readyCondition": {
        "status": "True"
      }
    }
  ],
  "bundledeployments": [
    {
      "name": "my-repo-bundle-cluster1",
      "namespace": "cluster-fleet-cluster1",
      "uid": "def-456-ghi",
      "generation": 2,
      "deploymentID": "s-ab12cd34:...",
      "appliedDeploymentID": "s-ab12cd34:...",
      "syncGeneration": 2,
      "forceSyncGeneration": 2,
      "commit": "abc1234567890def",
      "finalizers": ["fleet.cattle.io/bundledeployment-cleanup"],
      "readyCondition": {
        "status": "True"
      }
    }
  ],
  "contents": [
    {
      "name": "sha256-xyz789...",
      "sizeBytes": 102400,
      "finalizers": ["fleet.cattle.io/content-cleanup"],
      "deletionTimestamp": null
    }
  ],
  "clusters": [
    {
      "name": "cluster1",
      "namespace": "fleet-default",
      "agentStatus": "ready",
      "lastSeen": "2024-01-15T10:29:30Z",
      "agentAge": "2h30m",
      "bundleDeploymentsReady": 5,
      "bundleDeploymentsTotal": 5
    }
  ],
  "clustergroups": [
    {
      "name": "production",
      "namespace": "fleet-default",
      "clusterCount": 3,
      "selector": {"env": "prod"}
    }
  ],
  "diagnostics": {
    "bundlesWithGenerationMismatch": [...],
    "bundledeploymentsWithSyncGenerationMismatch": [...],
    "stuckBundleDeployments": [...],
    "gitRepoBundleInconsistencies": [...],
    "contentIssues": [...],
    "orphanedSecretsCount": 0,
    "invalidSecretOwnersCount": 0,
    "resourcesWithMultipleFinalizers": [...],
    "largeBundles": [...],
    "bundlesWithMissingContent": [...],
    "bundlesWithNoDeployments": [...],
    "gitReposWithNoBundles": [...],
    "clusterGroupsWithNoClusters": [...],
    "clustersWithAgentIssues": [...],
    "bundlesWithMissingGitRepo": [...],
    "bundleDeploymentsWithMissingBundle": [...],
    "gitReposWithGenerationMismatch": [...],
    "bundlesWithGenerationMismatch": [...]
  },
  "apiConsistency": {
    "consistent": true,
    "versions": ["12345"]
  }
}
```

## Command Options

```
Usage:
  fleet monitor [flags]

Flags:
  -n, --namespace string          Namespace to monitor (default: all namespaces)
      --system-namespace string   Fleet system namespace (default: cattle-fleet-system)
      --agent-staleness duration  Consider agent stale after this duration (default: 24h)
      --watch                     Watch for changes and output continuously
      --interval int              Interval in seconds between checks when watching (default: 60)
  -h, --help                      help for monitor
```

## Usage Examples

### Basic Monitoring

```bash
# Single snapshot with pretty formatting
fleet monitor | jq

# Monitor specific namespace
fleet monitor -n fleet-local | jq

# Check fleet-default namespace (common for local clusters)
fleet monitor -n fleet-default | jq
```

### Continuous Monitoring

```bash
# Collect snapshots every 60 seconds using watch mode
fleet monitor --watch --interval 60 >> monitor.json

# Or monitor with a shorter interval (every 30 seconds)
fleet monitor --watch --interval 30 >> monitor.json
```

### Targeted Diagnostics

```bash
# Check for stuck resources
fleet monitor | jq '.diagnostics | {
  bundlesWithGenerationMismatch: .bundlesWithGenerationMismatch | length,
  stuckBundleDeployments: .stuckBundleDeployments | length
}'

# Find bundles with old commits
fleet monitor | jq '.diagnostics.gitRepoBundleInconsistencies'

# Check agent health across all clusters
fleet monitor | jq '.diagnostics.clustersWithAgentIssues'

# Find large bundles that might impact etcd
fleet monitor | jq '.diagnostics.largeBundles'

# Check target matching issues
fleet monitor | jq '.diagnostics | {
  bundlesNoDeployments: .bundlesWithNoDeployments | length,
  gitreposNoBundles: .gitReposWithNoBundles | length,
  clusterGroupsNoClusters: .clusterGroupsWithNoClusters | length
}'
```

### Comparing States

```bash
# Before making changes
fleet monitor > before.json

# Make changes to GitRepo, bundles, etc.
kubectl edit gitrepo my-repo

# After changes
fleet monitor > after.json

# Compare
fleet analyze --compare after.json before.json
```

## Analyzing Monitor Output

The `fleet analyze` command provides powerful analysis capabilities for monitor output.

### Basic Analysis

```bash
# Show summary of latest snapshot
fleet analyze monitor.json

# Show only issues (useful for quick health checks)
fleet analyze --issues monitor.json

# Show detailed analysis with all information
fleet analyze --detailed monitor.json
```

### Multi-Snapshot Analysis

When your monitor.json file contains multiple snapshots (one JSON object per line):

```bash
# Show summary of latest snapshot only
fleet analyze monitor.json

# Show differences between consecutive snapshots
fleet analyze --diff monitor.json

# Show summary of all snapshots
fleet analyze --all monitor.json

# Compare two specific snapshot files
fleet analyze --compare snapshot2.json snapshot1.json
```

### Example Output

#### Summary Mode
```
┌─────────────────────────────────────────────────────────────┐
│ FLEET MONITORING SUMMARY - 2024-01-15T10:30:00Z            │
└─────────────────────────────────────────────────────────────┘

RESOURCE COUNTS:
  GitRepos: 5
  Bundles: 12 (Total Size: 2048KB)
  BundleDeployments: 36
  Contents: 8
  Clusters: 3
  ClusterGroups: 2

DIAGNOSTICS SUMMARY:
  Stuck Resources:
    - Stuck BundleDeployments: 1 ⚠
  Inconsistencies: 0 ✓
  Target Matching: 0 ✓
  Ownership Issues: 0 ✓
  Performance Issues: 1 ⚠
    - Large Bundles (>1MB): 1
  Agent Issues: 0 ✓
  Generation Mismatches: 1 ⚠
    - Bundles: 1
```

#### Issues-Only Mode
```
════════════════════════════════════════════════════════════════
FLEET ISSUES REPORT - 2024-01-15T10:30:00Z
════════════════════════════════════════════════════════════════

✗ BUNDLES WITH GENERATION MISMATCH (1):
  • fleet-default/my-repo-bundle
    Generation: 5 / Observed: 4
    Deletion Timestamp: none

✗ LARGE BUNDLES (1):
  • fleet-default/huge-bundle
    Size: 1.5MB
    Reason: Bundle size exceeds 1MB limit
```

#### Diff Mode
```
═══════════════════════════════════════════════════════════════
Changes: Snapshot 1 → 2
═══════════════════════════════════════════════════════════════

RESOURCE COUNT CHANGES:
  GitRepos: 5 → 5
  Bundles: 12 → 13 ⚠
  BundleDeployments: 36 → 39 ⚠

DIAGNOSTIC CHANGES:
  Bundles With Generation Mismatch: 1 → 0 ✓ DECREASED
  Stuck BundleDeployments: 1 → 0 ✓ DECREASED
  Large Bundles: 1 → 1

BUNDLE SIZE CHANGES:
  my-repo-bundle: 512KB → 256KB ✓ SHRUNK
```

## Common Troubleshooting Scenarios

### Scenario 1: Bundle Not Deploying

```bash
# Capture current state
fleet monitor | jq > bundle-status.json

# Check for bundles with generation mismatch
jq '.diagnostics.bundlesWithGenerationMismatch' bundle-status.json

# Check if bundle matched any targets
jq '.diagnostics.bundlesWithNoDeployments' bundle-status.json

# Check bundle-to-gitrepo consistency
jq '.diagnostics.gitRepoBundleInconsistencies' bundle-status.json
```

### Scenario 2: Agent Not Reporting Status

```bash
# Check agent health
fleet monitor | jq '.diagnostics.clustersWithAgentIssues'

# See detailed cluster info
fleet monitor | jq '.clusters[] | select(.agentStatus != "ready")'

# Check when agents last checked in
fleet monitor | jq '.clusters[] | {name, lastSeen, agentAge}'
```

### Scenario 3: Resources Stuck with Deletion Timestamps

```bash
# Find resources with deletion timestamps
fleet monitor | jq '{
  bundles: [.bundles[] | select(.deletionTimestamp != null) | .name],
  bundledeployments: [.bundledeployments[] | select(.deletionTimestamp != null) | .name]
}'

# Check finalizers preventing deletion
fleet monitor | jq '.bundles[] | select(.deletionTimestamp != null) | {name, finalizers}'
```

### Scenario 4: Commits Not Propagating

```bash
# Track commits through the lifecycle
fleet monitor | jq '{
  gitrepo: .gitrepos[0].commit[0:8],
  bundles: [.bundles[] | {name, commit: .commit[0:8]}],
  bundledeployments: [.bundledeployments[] | {name, commit: .commit[0:8]}]
}'

# Find commit mismatches
fleet monitor | jq '.diagnostics.gitRepoBundleInconsistencies[] | 
  select(.commitMismatch == true)'
```

### Scenario 5: Performance Issues

```bash
# Check bundle sizes
fleet monitor | jq '.diagnostics.largeBundles'

# Find bundles with most resources
fleet monitor | jq '[.bundles[] | {name, size: .sizeBytes, sizeMB: (.sizeBytes / 1048576 | floor)}] | 
  sort_by(.size) | reverse'

# Check for missing content resources
fleet monitor | jq '.diagnostics.bundlesWithMissingContent'
```

## Continuous Monitoring Workflow

For long-term monitoring and trend analysis:

```bash
# 1. Start continuous collection with watch mode (runs in background)
nohup fleet monitor --watch --interval 60 >> /var/log/fleet-monitor.json 2>&1 &

# 2. Periodically analyze for issues
watch -n 300 "fleet analyze --issues /var/log/fleet-monitor.json | tail -30"

# 3. Generate daily reports
fleet analyze --diff /var/log/fleet-monitor.json > fleet-report-$(date +%Y%m%d).txt

# 4. Log rotation (keep last 7 days)
find /var/log -name "fleet-report-*.txt" -mtime +7 -delete
```

## Integration with Alerting

The monitor command can be integrated with monitoring systems:

```bash
# Check if there are any issues (exit code 0 = healthy, 1 = issues)
if fleet monitor | jq -e '
  .diagnostics.stuckBundles != [] or
  .diagnostics.stuckBundleDeployments != [] or
  .diagnostics.clustersWithAgentIssues != []
' > /dev/null; then
  echo "ALERT: Fleet issues detected!"
  fleet monitor | jq '.diagnostics' | mail -s "Fleet Alert" admin@example.com
fi

# Prometheus-style metrics export
fleet monitor | jq -r '
  "fleet_stuck_bundles \(.diagnostics.stuckBundles | length)",
  "fleet_stuck_bundledeployments \(.diagnostics.stuckBundleDeployments | length)",
  "fleet_agent_issues \(.diagnostics.clustersWithAgentIssues | length)",
  "fleet_large_bundles \(.diagnostics.largeBundles | length)"
'
```

## Understanding Diagnostics

### Stuck Resources

A resource is considered "stuck" when:

**Bundle (Generation Mismatch):**
- `generation != observedGeneration` (controller hasn't processed latest spec)

**Note:** Bundles with deletion timestamps are tracked separately as a count in `diagnostics.bundlesWithDeletionTimestamp`.

**BundleDeployment (Stuck):**
- `spec.deploymentID != status.appliedDeploymentID` (agent hasn't applied latest deployment)
- `syncGeneration` doesn't match `forceSyncGeneration` (forced sync not applied)
- Has `deletionTimestamp` but still exists

**BundleDeployment (SyncGeneration Mismatch):**
- `syncGeneration` != `forceSyncGeneration` when `forceSyncGeneration > 0` (tracked separately from stuck BundleDeployments)

### API Consistency Check

The monitor performs multiple fetches of the same resources to detect "time travel" - when the Kubernetes API server returns different resource versions due to stale caching. This is critical because stale data can make bundles appear stuck when they're actually progressing.

### Commit Tracking

The monitor tracks Git commit hashes through the entire lifecycle:
1. **GitRepo** fetches latest commit
2. **Bundle** should reflect that commit
3. **BundleDeployment** should match Bundle's commit
4. **Bundle Secrets** store commit in annotations

Mismatches indicate where the sync process is failing.

## Performance Considerations

- The monitor fetches all Fleet resources in the cluster
- For large installations (1000+ bundles), consider:
  - Using `--namespace` to limit scope
  - Running less frequently (e.g., every 120+ seconds instead of 60)
  - Monitoring resource usage of the monitor command itself

## Troubleshooting the Monitor Command

If the monitor command fails:

```bash
# Check Fleet controller is running
kubectl get pods -n cattle-fleet-system

# Verify you have proper RBAC permissions
kubectl auth can-i list bundles --all-namespaces
kubectl auth can-i list bundledeployments --all-namespaces

# Check if CRDs are installed
kubectl get crds | grep fleet.cattle.io

# Enable verbose logging
fleet monitor --verbose 2>&1 | tee monitor-debug.log
```

## Analyze Command

The `fleet analyze` command provides human-readable analysis of monitor snapshots.

### Usage

```bash
# Analyze latest snapshot
fleet analyze monitor.json

# Analyze from stdin
fleet monitor | fleet analyze

# Show all snapshots in file
fleet analyze --all monitor.json

# Show changes between consecutive snapshots
fleet analyze --diff monitor.json

# Show only resources with issues
fleet analyze --issues monitor.json

# Detailed analysis with controller info and events
fleet analyze --detailed monitor.json

# Compare two specific snapshot files
fleet analyze --compare before.json after.json

# JSON output for programmatic use
fleet analyze --json monitor.json

# Disable colored output
fleet analyze --no-color monitor.json
```

### Output Modes

- **Summary (default)**: High-level overview of resources and diagnostics
- **All**: Summary of all snapshots in a multi-snapshot file
- **Diff**: Shows changes between consecutive snapshots
- **Issues**: Shows only resources with detected problems
- **Detailed**: Complete analysis including controller info, API consistency, and events
- **JSON**: Machine-readable output for scripts and automation

### Benefits Over Bash Script

- **No Dependencies**: Single binary, no bash/jq required
- **Cross-Platform**: Works on Windows, Linux, and macOS
- **Type Safe**: No JSON parsing errors
- **Faster**: Direct processing without subprocess overhead
- **Integrated**: Shares types with monitor command
- **Better Errors**: Clear error messages

### Command Examples

Common analysis commands:

| Task | Command |
|------|---------|
| Basic summary | `fleet analyze monitor.json` |
| Show changes over time | `fleet analyze --diff monitor.json` |
| Only show issues | `fleet analyze --issues monitor.json` |
| Detailed analysis | `fleet analyze --detailed monitor.json` |
| Compare two snapshots | `fleet analyze --compare after.json before.json` |

## Building Fleet Util

The monitor and analyze commands are built as part of the Fleet CLI:

```bash
# Build the fleetcli binary
go build -o fleet ./cmd/fleetcli

# Run monitor
./fleet monitor | jq

# Analyze snapshots
./fleet analyze monitor.json
```

## Creating Releases

To create releases of fleet for distribution:

1. Fork the Fleet repository on GitHub
2. The `.github/workflows/release-monitor.yml` workflow is already configured
3. Create a Git tag with the `util-` prefix and push:
   ```bash
   git tag -a util-v1.0.0 -m "Release fleet v1.0.0"
   git push origin util-v1.0.0
   ```
4. GitHub Actions will automatically:
   - Build binaries for Linux (amd64/arm64), macOS (amd64/arm64), and Windows (amd64)
   - Generate SHA256 checksums
   - Create a GitHub release with all artifacts using `gh` CLI
5. Download binaries from the GitHub releases page

## Contributing

To contribute improvements to the monitor or analyze commands:

1. **Monitor Source Code**: 
   - `internal/cmd/cli/monitor.go` - Main monitor command
   - `internal/cmd/cli/monitor_diagnostics.go` - Diagnostic detection logic
2. **Analyze Source Code**:
   - `internal/cmd/cli/analyze.go` - Analysis and visualization
3. **Tests**:
   - Integration tests: `integrationtests/cli/monitor/`
4. **Development**:
   - Follow the patterns in `AGENTS.md` for code structure
   - Add new diagnostics to the `detectIssues()` function in monitor
   - Add new analysis modes to analyze command as needed
   - Update this README with new diagnostic types or features

## See Also

- [Fleet Documentation](https://fleet.rancher.io/)
- [DESIGN.md](./DESIGN.md) - Fleet architecture and design decisions
- [AGENTS.md](./AGENTS.md) - AI agent guide with development patterns
- [Fleet Troubleshooting Guide](https://fleet.rancher.io/troubleshooting)
