#!/bin/bash

# Fleet Monitoring Analysis Script
# Analyzes JSON output from fleetcli monitor command
# Can process single snapshots or multiple snapshots to detect changes over time
#
# USAGE:
#
#   Single snapshot analysis:
#     fleetcli monitor | ./analyze-fleet-monitoring.sh
#     ./analyze-fleet-monitoring.sh snapshot.json
#
#   Multiple snapshots (one JSON per line):
#     ./analyze-fleet-monitoring.sh monitor.json              # Shows latest snapshot
#     ./analyze-fleet-monitoring.sh --all monitor.json        # Shows all snapshots
#     ./analyze-fleet-monitoring.sh --diff monitor.json       # Shows changes between snapshots
#
#   Continuous monitoring:
#     while true; do
#       fleetcli monitor >> monitor.json
#       sleep 10
#     done &
#     ./analyze-fleet-monitoring.sh --live monitor.json       # Live updates
#
#   Compare two specific snapshots:
#     ./analyze-fleet-monitoring.sh --compare snapshot1.json snapshot2.json
#
# OUTPUT MODES:
#   --summary, -s   Show summary of latest snapshot (default)
#   --all           Show summary of all snapshots in file
#   --diff, -d      Show changes between consecutive snapshots
#   --live          Monitor file for new snapshots and show updates
#   --compare       Compare two specific snapshot files
#   --issues, -i    Show only resources with issues
#   --detailed      Show detailed analysis
#   --json          Output in JSON format for programmatic use
#
# EXAMPLES:
#
#   # Collect snapshots every 10 seconds for 5 minutes
#   for i in {1..30}; do
#     fleetcli monitor >> monitor.json
#     sleep 10
#   done
#
#   # Analyze the snapshots
#   ./analyze-fleet-monitoring.sh --diff monitor.json
#
#   # Watch for issues in real-time
#   ./analyze-fleet-monitoring.sh --live monitor.json

set -euo pipefail

MODE="${1:-summary}"
INPUT_FILE="${2:-}"

# Colors
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

print_header() {
    echo -e "\n${BOLD}${BLUE}=== $1 ===${NC}\n"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_info() {
    echo -e "${CYAN}ℹ $1${NC}"
}

# Summary analysis - high-level overview
summary_analysis() {
    jq -r '
        "┌─────────────────────────────────────────────────────────────┐",
        "│ FLEET MONITORING SUMMARY - \(.timestamp)             │",
        "└─────────────────────────────────────────────────────────────┘",
        "",
        "RESOURCE COUNTS:",
        "  GitRepos: \(.gitrepos | length)",
        "  Bundles: \(.bundles | length) (Total Size: \([.bundles[].sizeBytes // 0] | add | . / 1024 | floor)KB)",
        "  BundleDeployments: \(.bundledeployments | length)",
        "  Contents: \(.contents | length)",
        "  Clusters: \(.clusters | length)",
        "  ClusterGroups: \(.clusterGroups | length)",
        "",
        "DIAGNOSTICS SUMMARY:",
        "  Stuck Resources:",
        "    - Stuck BundleDeployments: \((.diagnostics.stuckBundleDeployments // []) | length) \(if ((.diagnostics.stuckBundleDeployments // []) | length) > 0 then "⚠" else "✓" end)",
        "  Inconsistencies: \(((.diagnostics.gitRepoBundleInconsistencies // []) | length) + ((.diagnostics.contentIssues // []) | length)) \(if (((.diagnostics.gitRepoBundleInconsistencies // []) | length) + ((.diagnostics.contentIssues // []) | length)) > 0 then "⚠" else "✓" end)",
        "    - GitRepo/Bundle Mismatches: \((.diagnostics.gitRepoBundleInconsistencies // []) | length)",
        "    - Content Issues: \((.diagnostics.contentIssues // []) | length)",
        "  Target Matching: \(((.diagnostics.bundlesWithNoDeployments // []) | length) + ((.diagnostics.gitReposWithNoBundles // []) | length) + ((.diagnostics.clusterGroupsWithNoClusters // []) | length)) \(if (((.diagnostics.bundlesWithNoDeployments // []) | length) + ((.diagnostics.gitReposWithNoBundles // []) | length) + ((.diagnostics.clusterGroupsWithNoClusters // []) | length)) > 0 then "⚠" else "✓" end)",
        "    - Bundles with No Deployments: \((.diagnostics.bundlesWithNoDeployments // []) | length)",
        "    - GitRepos with No Bundles: \((.diagnostics.gitReposWithNoBundles // []) | length)",
        "    - ClusterGroups with No Clusters: \((.diagnostics.clusterGroupsWithNoClusters // []) | length)",
        "  Ownership Issues: \(((.diagnostics.bundlesWithMissingGitRepo // []) | length) + ((.diagnostics.bundleDeploymentsWithMissingBundle // []) | length) + ((.diagnostics.resourcesWithMultipleFinalizers // []) | length)) \(if (((.diagnostics.bundlesWithMissingGitRepo // []) | length) + ((.diagnostics.bundleDeploymentsWithMissingBundle // []) | length) + ((.diagnostics.resourcesWithMultipleFinalizers // []) | length)) > 0 then "⚠" else "✓" end)",
        "    - Bundles with Missing GitRepo: \((.diagnostics.bundlesWithMissingGitRepo // []) | length)",
        "    - BundleDeployments with Missing Bundle: \((.diagnostics.bundleDeploymentsWithMissingBundle // []) | length)",
        "    - Resources with Multiple Finalizers: \((.diagnostics.resourcesWithMultipleFinalizers // []) | length)",
        "  Performance Issues: \(((.diagnostics.largeBundles // []) | length) + ((.diagnostics.bundlesWithMissingContent // []) | length)) \(if (((.diagnostics.largeBundles // []) | length) + ((.diagnostics.bundlesWithMissingContent // []) | length)) > 0 then "⚠" else "✓" end)",
        "    - Large Bundles (>1MB): \((.diagnostics.largeBundles // []) | length)",
        "    - Bundles with Missing Content: \((.diagnostics.bundlesWithMissingContent // []) | length)",
        "  Agent Issues: \((.diagnostics.clustersWithAgentIssues // []) | length) \(if ((.diagnostics.clustersWithAgentIssues // []) | length) > 0 then "⚠" else "✓" end)",
        "  Generation Mismatches: \(((.diagnostics.gitReposWithGenerationMismatch // []) | length) + ((.diagnostics.bundlesWithGenerationMismatch // []) | length) + ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length)) \(if (((.diagnostics.gitReposWithGenerationMismatch // []) | length) + ((.diagnostics.bundlesWithGenerationMismatch // []) | length) + ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length)) > 0 then "⚠" else "✓" end)",
        "    - GitRepos: \((.diagnostics.gitReposWithGenerationMismatch // []) | length)",
        "    - Bundles: \((.diagnostics.bundlesWithGenerationMismatch // []) | length)",
        "    - BundleDeployments (syncGeneration): \((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length)",
        ""
    '
}

# Detailed analysis - show everything including issues
detailed_analysis() {
    jq -r '
        def format_commit(c):
            if c == null or c == "none" then "N/A"
            else c[0:8]
            end;

        "════════════════════════════════════════════════════════════════",
        "DETAILED FLEET ANALYSIS - \(.timestamp)",
        "════════════════════════════════════════════════════════════════",
        "",
        "╔═══ CONTROLLER ═══╗",
        "  Name: \(.controller.name)",
        "  Status: \(.controller.status)",
        "  Restarts: \(.controller.restarts) \(if .controller.restarts > 0 then "⚠ RESTARTED" else "" end)",
        "  Started: \(.controller.startTime)",
        "",
        (if ((.diagnostics.bundlesWithGenerationMismatch // []) | length) > 0 then
            "╔═══ BUNDLES WITH GENERATION MISMATCH ⚠ ═══╗",
            ((.diagnostics.bundlesWithGenerationMismatch // [])[] |
                "  Bundle: \(.namespace)/\(.name)",
                "    Generation: \(.generation) / Observed: \(.observedGeneration)",
                "    Deletion Timestamp: \(.deletionTimestamp // "none")",
                "    Reasons: \((.reasons // []) | join(", "))",
                "    Ready Condition: \(.readyCondition.status // "N/A") - \(.readyCondition.message // "")",
                ""
            ),
            ""
        else "" end),
        (if ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length) > 0 then
            "╔═══ BUNDLEDEPLOYMENTS WITH SYNCGENERATION MISMATCH ⚠ ═══╗",
            ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // [])[] |
                "  BundleDeployment: \(.namespace)/\(.name)",
                "    ForceSyncGeneration: \(.forceSyncGeneration) / SyncGeneration: \(.syncGeneration // "nil")",
                "    DeploymentID: \(.deploymentID)",
                "    AppliedDeploymentID: \(.appliedDeploymentID)",
                ""
            ),
            ""
        else "" end),
        (if ((.diagnostics.stuckBundleDeployments // []) | length) > 0 then
            "╔═══ STUCK BUNDLEDEPLOYMENTS ⚠ ═══╗",
            ((.diagnostics.stuckBundleDeployments // [])[] |
                "  BundleDeployment: \(.namespace)/\(.name)",
                "    Generation: \(.generation) / Observed: \(.observedGeneration // "N/A")",
                "    DeploymentID: \(.deploymentID[0:50])...",
                "    AppliedID:    \(.appliedDeploymentID[0:50])...",
                "    Match: \(if .deploymentID == .appliedDeploymentID then "YES" else "NO ⚠" end)",
                "    Deletion Timestamp: \(.deletionTimestamp // "none")",
                "    Reasons: \((.reasons // ["agent not applying"]) | join(", "))",
                "    Ready Condition: \(.readyCondition.status // "N/A") - \(.readyCondition.message // "")",
                ""
            ),
            ""
        else "" end),
        (if (.diagnostics.orphanedSecrets // []) | length > 0 then
            "╔═══ ORPHANED SECRETS ⚠ ═══╗",
            (.orphanedSecrets[] |
                "  Secret: \(.namespace)/\(.name)",
                "    Type: \(.type)",
                "    Reason: \(.reason)",
                "    Deletion Timestamp: \(.deletionTimestamp // "none")",
                "    Owner: \(.ownerReferences[0].kind)/\(.ownerReferences[0].name) (UID: \(.ownerReferences[0].uid[0:13]))",
                ""
            ),
            ""
        else "" end),
        (if (.diagnostics.invalidSecretOwnersCount // 0) > 0 then
            "╔═══ INVALID SECRET OWNERS ⚠ ═══╗",
            ((.diagnostics.invalidSecretOwners // [])[] |
                "  Secret: \(.namespace)/\(.name)",
                "    Type: \(.type)",
                "    Issue: \(.issue)",
                "    Owner: \(.ownerKind)/\(.ownerName)",
                "    Owner UID: \(.ownerUID[0:13])",
                "    Expected UID: \((.expectedUID // "NOT FOUND")[0:13])",
                ""
            ),
            ""
        else "" end),
        (if ((.diagnostics.bundlesWithDeletionTimestamp // 0) + (.diagnostics.bundleDeploymentsWithDeletionTimestamp // 0)) > 0 then
            "╔═══ RESOURCES WITH DELETION TIMESTAMPS ⚠ ═══╗",
            ((.bundles // [])[] | select(.deletionTimestamp != null) |
                "  Bundle: \(.namespace)/\(.name)",
                "    Deletion Timestamp: \(.deletionTimestamp)",
                "    Finalizers: \((.finalizers // []) | join(", "))",
                ""
            ),
            ((.bundledeployments // [])[] | select(.deletionTimestamp != null) |
                "  BundleDeployment: \(.namespace)/\(.name)",
                "    Deletion Timestamp: \(.deletionTimestamp)",
                "    Finalizers: \((.finalizers // []) | join(", "))",
                ""
            ),
            ""
        else "" end),
        (if (.apiConsistency.consistent // true) == false then
            "╔═══ API CONSISTENCY ISSUES ⚠ ═══╗",
            "  API Server returned different resource versions!",
            "  Versions: \((.apiConsistency.versions // []) | join(", "))",
            "  This indicates the API server may be returning stale data.",
            ""
        else "" end),
        (if (.recentEvents | length) > 0 then
            "╔═══ RECENT EVENTS ═══╗",
            (.recentEvents[] |
                "  [\(.type)] \(.involvedObject.kind)/\(.involvedObject.name)",
                "    Reason: \(.reason)",
                "    Message: \(.message)",
                "    Count: \(.count) | Last: \(.lastTimestamp)",
                ""
            ),
            ""
        else "" end),
        "╔═══ COMMIT TRACKING ═══╗",
        "GitRepos:",
        (.gitrepos[] | "  \(.name): \(.commit | format_commit)"),
        "",
        "Bundles:",
        (.bundles[] | select(.commit != null) | "  \(.name): \(.commit | format_commit) (UID: \(.uid[0:13]))"),
        "",
        "BundleDeployments:",
        (.bundledeployments[] | select(.commit != null) | "  \(.name): \(.commit | format_commit) (UID: \(.uid[0:13]))"),
        "",
        "Bundle Secrets:",
        (.bundleSecrets[] | "  \(.name) (\(if .type == "fleet.cattle.io/bundle-values/v1alpha1" then "values" else "deployment" end)): \((.commit | format_commit) // "N/A")"),
        ""
    '
}

# Issues-only analysis - only show problems
issues_only() {
    jq -r '
        def format_commit(c):
            if c == null or c == "none" then "N/A"
            else c[0:8]
            end;

        "════════════════════════════════════════════════════════════════",
        "FLEET ISSUES REPORT - \(.timestamp)",
        "════════════════════════════════════════════════════════════════",
        "",
        (if .controller.restarts > 0 then
            "⚠ CONTROLLER RESTARTED:",
            "  Restarts: \(.controller.restarts)",
            "  Current Start Time: \(.controller.startTime)",
            ""
        else "" end),
        (if ((.diagnostics.bundlesWithGenerationMismatch // []) | length) > 0 then
            "✗ BUNDLES WITH GENERATION MISMATCH (\((.diagnostics.bundlesWithGenerationMismatch // []) | length)):",
            ((.diagnostics.bundlesWithGenerationMismatch // [])[] |
                "  • \(.namespace)/\(.name)",
                "    Generation: \(.generation) / Observed: \(.observedGeneration)",
                "    Deletion Timestamp: \(.deletionTimestamp // "none")",
                ""
            )
        else "" end),
        (if ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length) > 0 then
            "✗ BUNDLEDEPLOYMENTS WITH SYNCGENERATION MISMATCH (\((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length)):",
            ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // [])[] |
                "  • \(.namespace)/\(.name)",
                "    ForceSyncGeneration: \(.forceSyncGeneration) / SyncGeneration: \(.syncGeneration // "nil")",
                ""
            )
        else "" end),
        (if ((.diagnostics.stuckBundleDeployments // []) | length) > 0 then
            "✗ STUCK BUNDLEDEPLOYMENTS (\((.diagnostics.stuckBundleDeployments // []) | length)):",
            ((.diagnostics.stuckBundleDeployments // [])[] |
                "  • \(.namespace)/\(.name)",
                "    Reasons: \((.reasons // ["agent not applying"]) | join(", "))",
                "    Gen: \(.generation) | DepID Match: \(if .deploymentID == .appliedDeploymentID then "YES" else "NO" end)",
                ""
            )
        else "" end),
        (if (.diagnostics.gitrepoBundleInconsistenciesCount // 0) > 0 then
            "✗ GITREPO/BUNDLE INCONSISTENCIES (\(.diagnostics.gitrepoBundleInconsistenciesCount)):",
            ((.diagnostics.gitrepoBundleInconsistencies // [])[] as $bundle |
                # Find the corresponding GitRepo using the repo-name label
                ((.gitrepos // []) | map(select(.name == ($bundle.repoName // "") and .namespace == $bundle.namespace)) | first) as $gitrepo |
                "  • Bundle: \($bundle.namespace)/\($bundle.name) (repo: \($bundle.repoName // "N/A"))",
                (if $gitrepo then
                    (if ($bundle.commit // "") != ($gitrepo.commit // "") then
                        "    ⚠ Commit Mismatch: Bundle=\(($bundle.commit // "N/A") | if . == "N/A" then . else .[0:8] end) | GitRepo=\(($gitrepo.commit // "N/A") | if . == "N/A" then . else .[0:8] end)"
                    else
                        "    Commit: \(($bundle.commit // "N/A") | if . == "N/A" then . else .[0:8] end)"
                    end),
                    (if ($bundle.forceSyncGeneration // 0) != ($gitrepo.forceSyncGeneration // 0) then
                        "    ⚠ ForceSyncGeneration Mismatch: Bundle=\($bundle.forceSyncGeneration // 0) | GitRepo=\($gitrepo.forceSyncGeneration // 0)"
                    else
                        "    ForceSyncGeneration: \($bundle.forceSyncGeneration // 0)"
                    end)
                else
                    "    Generation: \($bundle.generation // "N/A") / Observed: \($bundle.observedGeneration // "N/A")",
                    "    Commit: \(($bundle.commit // "N/A") | if . == "N/A" then . else .[0:8] end)",
                    "    ForceSyncGeneration: \($bundle.forceSyncGeneration // 0)",
                    "    (GitRepo not found)"
                end),
                "    Ready: \(if $bundle.ready then "Yes" else "No - \($bundle.readyMessage // "unknown")" end)",
                ""
            )
        else "" end),
        (if (.diagnostics.contentIssuesCount // 0) > 0 then
            "✗ CONTENT RESOURCE ISSUES (\(.diagnostics.contentIssuesCount)):",
            ((.diagnostics.contentIssues // [])[] |
                "  • BundleDeployment: \(.namespace)/\(.name)",
                "    Content: \(.contentName[0:30])...",
                "    Issues: \((.issues // []) | join(", "))",
                "    Content Exists: \(.contentExists) | DeletionTimestamp: \(.contentDeletionTimestamp // "none")",
                "    Finalizers: \((.contentFinalizers // []) | join(", "))",
                ""
            )
        else "" end),
        (if (.diagnostics.orphanedSecretsCount // 0) > 0 then
            "⚠ ORPHANED SECRETS (\(.diagnostics.orphanedSecretsCount)):",
            ((.diagnostics.orphanedSecrets // [])[] |
                "  • \(.namespace)/\(.name)",
                "    Type: \(.type) | Reason: \(.reason)",
                "    DelTime: \(.deletionTimestamp // "none")",
                ""
            )
        else "" end),
        (if (.diagnostics.invalidSecretOwnersCount // 0) > 0 then
            "✗ INVALID SECRET OWNERS (\(.diagnostics.invalidSecretOwnersCount)):",
            ((.diagnostics.invalidSecretOwners // [])[] |
                "  • \(.namespace)/\(.name)",
                "    Issue: \(.issue) | Owner: \(.ownerKind)/\(.ownerName)",
                "    UID Mismatch: \(.ownerUID[0:13]) vs \((.expectedUID // "N/A")[0:13])",
                ""
            )
        else "" end),
        (if (([.bundles[]? | select(.deletionTimestamp != null)]) | length) > 0 then
            "⚠ BUNDLES WITH DELETION TIMESTAMP (\(([.bundles[]? | select(.deletionTimestamp != null)]) | length)):",
            ((.bundles // [])[] | select(.deletionTimestamp != null) |
                "  • \(.namespace)/\(.name)",
                "    DelTime: \(.deletionTimestamp)",
                "    Finalizers: \((.finalizers // []) | join(", "))",
                ""
            )
        else "" end),
        (if (([.bundledeployments[]? | select(.deletionTimestamp != null)]) | length) > 0 then
            "⚠ BUNDLEDEPLOYMENTS WITH DELETION TIMESTAMP (\(([.bundledeployments[]? | select(.deletionTimestamp != null)]) | length)):",
            ((.bundledeployments // [])[] | select(.deletionTimestamp != null) |
                "  • \(.namespace)/\(.name)",
                "    DelTime: \(.deletionTimestamp)",
                "    Finalizers: \((.finalizers // []) | join(", "))",
                ""
            )
        else "" end),
        (if (([.contents[]? | select(.deletionTimestamp != null)]) | length) > 0 then
            "⚠ CONTENTS WITH DELETION TIMESTAMP (\(([.contents[]? | select(.deletionTimestamp != null)]) | length)):",
            ((.contents // [])[] | select(.deletionTimestamp != null) |
                "  • \(.name[0:40])...",
                "    DelTime: \(.deletionTimestamp)",
                "    Finalizers: \((.finalizers // []) | join(", "))",
                ""
            )
        else "" end),
        (if (.apiConsistency.consistent // true) == false then
            "✗ API CONSISTENCY FAILURE:",
            "  Different resource versions returned: \((.apiConsistency.versions // []) | join(", "))",
            "  This indicates the API server is returning stale cached data!",
            ""
        else "" end),
        (if (((.diagnostics.bundlesWithGenerationMismatch // []) | length) == 0 and
            ((.diagnostics.bundledeploymentsWithSyncGenerationMismatch // []) | length) == 0 and
            ((.diagnostics.stuckBundleDeployments // []) | length) == 0 and
            ((.diagnostics.gitrepoBundleInconsistencies // []) | length) == 0 and
            ((.diagnostics.contentIssues // []) | length) == 0 and
            ((.diagnostics.orphanedSecrets // []) | length) == 0 and
            ((.diagnostics.invalidSecretOwners // []) | length) == 0 and
            ((.bundles // []) | map(select(.deletionTimestamp != null)) | length) == 0 and
            ((.bundledeployments // []) | map(select(.deletionTimestamp != null)) | length) == 0 and
            ((.contents // []) | map(select(.deletionTimestamp != null)) | length) == 0 and
            (.apiConsistency.consistent // true) == true and
            (.controller.restarts // 0) == 0) then
            "✓ NO ISSUES DETECTED - All systems healthy!",
            ""
        else "" end)
    '
}

# Compare two snapshots
compare_snapshots() {
    local file1="$1"
    local file2="$2"

    jq -n --slurpfile before "$file1" --slurpfile after "$file2" '
        def format_commit(c):
            if c == null or c == "none" then "N/A"
            else c[0:8]
            end;

        $before[0] as $b | $after[0] as $a |

        "════════════════════════════════════════════════════════════════",
        "FLEET CHANGES COMPARISON",
        "════════════════════════════════════════════════════════════════",
        "Before: \($b.timestamp)",
        "After:  \($a.timestamp)",
        "",
        "╔═══ CONTROLLER CHANGES ═══╗",
        (if $b.controller.name != $a.controller.name then
            "  Pod Changed: \($b.controller.name) → \($a.controller.name) ⚠ POD RESTARTED!"
        else
            "  Pod: \($a.controller.name) (unchanged)"
        end),
        (if $b.controller.restarts != $a.controller.restarts then
            "  Restarts: \($b.controller.restarts) → \($a.controller.restarts) ⚠ INCREASED!"
        else
            "  Restarts: \($a.controller.restarts) (unchanged)"
        end),
        "",
        "╔═══ GITREPO CHANGES ═══╗",
        ([$a.gitrepos[] | .name] - [$b.gitrepos[] | .name] | if length > 0 then
            "  New GitRepos: \(. | join(", "))"
        else "" end),
        ([$b.gitrepos[] | .name] - [$a.gitrepos[] | .name] | if length > 0 then
            "  Deleted GitRepos: \(. | join(", "))"
        else "" end),
        ($b.gitrepos[] | . as $bg | $a.gitrepos[] | select(.name == $bg.name) | . as $ag |
            if $bg.commit != $ag.commit or $bg.generation != $ag.generation or $bg.forceSyncGeneration != $ag.forceSyncGeneration then
                "  \($ag.name):",
                (if $bg.commit != $ag.commit then "    Commit: \($bg.commit | format_commit) → \($ag.commit | format_commit)" else "" end),
                (if $bg.generation != $ag.generation then "    Generation: \($bg.generation) → \($ag.generation)" else "" end),
                (if $bg.observedGeneration != $ag.observedGeneration then "    ObservedGen: \($bg.observedGeneration) → \($ag.observedGeneration)" else "" end),
                (if $bg.forceSyncGeneration != $ag.forceSyncGeneration then "    ForceSyncGen: \($bg.forceSyncGeneration // "N/A") → \($ag.forceSyncGeneration // "N/A")" else "" end)
            else "" end
        ),
        "",
        "╔═══ BUNDLE CHANGES ═══╗",
        ([$a.bundles[] | .name] - [$b.bundles[] | .name] | if length > 0 then
            "  New Bundles: \(. | join(", "))"
        else "" end),
        ([$b.bundles[] | .name] - [$a.bundles[] | .name] | if length > 0 then
            "  Deleted Bundles: \(. | join(", "))"
        else "" end),
        ($b.bundles[] | . as $bb | $a.bundles[] | select(.name == $bb.name) | . as $ab |
            if $bb.commit != $ab.commit or $bb.generation != $ab.generation or $bb.uid != $ab.uid or $bb.deletionTimestamp != $ab.deletionTimestamp then
                "  \($ab.name):",
                (if $bb.commit != $ab.commit then "    Commit: \(($bb.commit | format_commit) // "N/A") → \(($ab.commit | format_commit) // "N/A")" else "" end),
                (if $bb.generation != $ab.generation then "    Generation: \($bb.generation) → \($ab.generation)" else "" end),
                (if $bb.observedGeneration != $ab.observedGeneration then "    ObservedGen: \($bb.observedGeneration) → \($ab.observedGeneration)" else "" end),
                (if $bb.uid != $ab.uid then "    UID CHANGED: \($bb.uid[0:13]) → \($ab.uid[0:13]) ⚠ RECREATED!" else "" end),
                (if $bb.deletionTimestamp != $ab.deletionTimestamp then "    DeletionTimestamp: \($bb.deletionTimestamp // "none") → \($ab.deletionTimestamp // "none")" else "" end)
            else "" end
        ),
        "",
        "╔═══ BUNDLEDEPLOYMENT CHANGES ═══╗",
        ([$a.bundledeployments[] | .name] - [$b.bundledeployments[] | .name] | if length > 0 then
            "  New BundleDeployments: \(. | join(", "))"
        else "" end),
        ([$b.bundledeployments[] | .name] - [$a.bundledeployments[] | .name] | if length > 0 then
            "  Deleted BundleDeployments: \(. | join(", "))"
        else "" end),
        ($b.bundledeployments[] | . as $bbd | $a.bundledeployments[] | select(.name == $bbd.name) | . as $abd |
            if $bbd.commit != $abd.commit or $bbd.deploymentID != $abd.deploymentID or $bbd.uid != $abd.uid or $bbd.deletionTimestamp != $abd.deletionTimestamp then
                "  \($abd.name):",
                (if $bbd.commit != $abd.commit then "    Commit: \(($bbd.commit | format_commit) // "N/A") → \(($abd.commit | format_commit) // "N/A")" else "" end),
                (if $bbd.deploymentID != $abd.deploymentID then "    DeploymentID changed" else "" end),
                (if $bbd.appliedDeploymentID != $abd.appliedDeploymentID then "    AppliedDeploymentID changed" else "" end),
                (if $bbd.uid != $abd.uid then "    UID CHANGED: \($bbd.uid[0:13]) → \($abd.uid[0:13]) ⚠ RECREATED!" else "" end),
                (if $bbd.deletionTimestamp != $abd.deletionTimestamp then "    DeletionTimestamp: \($bbd.deletionTimestamp // "none") → \($abd.deletionTimestamp // "none")" else "" end)
            else "" end
        ),
        "",
        "╔═══ DIAGNOSTICS CHANGES ═══╗",
        "  Bundles With Generation Mismatch: \($b.diagnostics.bundlesWithGenerationMismatch | length) → \($a.diagnostics.bundlesWithGenerationMismatch | length) \(if ($a.diagnostics.bundlesWithGenerationMismatch | length) > ($b.diagnostics.bundlesWithGenerationMismatch | length) then "⚠ INCREASED" else "" end)",
        "  BundleDeployments With SyncGeneration Mismatch: \($b.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) → \($a.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) \(if ($a.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) > ($b.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) then "⚠ INCREASED" else "" end)",
        "  Stuck BundleDeployments: \($b.diagnostics.stuckBundleDeployments | length) → \($a.diagnostics.stuckBundleDeployments | length) \(if ($a.diagnostics.stuckBundleDeployments | length) > ($b.diagnostics.stuckBundleDeployments | length) then "⚠ INCREASED" else "" end)",
        "  Orphaned Secrets: \($b.diagnostics.orphanedSecretsCount) → \($a.diagnostics.orphanedSecretsCount) \(if $a.diagnostics.orphanedSecretsCount > $b.diagnostics.orphanedSecretsCount then "⚠ INCREASED" else "" end)",
        "  Invalid Secret Owners: \($b.diagnostics.invalidSecretOwnersCount) → \($a.diagnostics.invalidSecretOwnersCount) \(if $a.diagnostics.invalidSecretOwnersCount > $b.diagnostics.invalidSecretOwnersCount then "⚠ INCREASED" else "" end)",
        ""
    '
}

# JSON output for programmatic use
json_output() {
    jq '{
        timestamp,
        summary: {
            controllerRestarts: .controller.restarts,
            gitrepoCount: (.gitrepos | length),
            bundleCount: (.bundles | length),
            bundleDeploymentCount: (.bundledeployments | length),
            secretCount: (.bundleSecrets | length),
            bundlesWithGenerationMismatchCount: (.diagnostics.bundlesWithGenerationMismatch | length),
            bundleDeploymentsWithSyncGenerationMismatchCount: (.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length),
            stuckBundleDeploymentsCount: (.diagnostics.stuckBundleDeployments | length),
            orphanedSecretsCount: .diagnostics.orphanedSecretsCount,
            invalidSecretOwnersCount: .diagnostics.invalidSecretOwnersCount,
            apiConsistent: .apiConsistency.consistent,
            hasIssues: (
                (.diagnostics.bundlesWithGenerationMismatch | length) > 0 or
                (.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) > 0 or
                (.diagnostics.stuckBundleDeployments | length) > 0 or
                .diagnostics.orphanedSecretsCount > 0 or
                .diagnostics.invalidSecretOwnersCount > 0 or
                .diagnostics.bundlesWithDeletionTimestamp > 0 or
                .diagnostics.bundleDeploymentsWithDeletionTimestamp > 0 or
                .apiConsistency.consistent == false
            )
        },
        issues: {
            bundlesWithGenerationMismatch: .diagnostics.bundlesWithGenerationMismatch,
            bundleDeploymentsWithSyncGenerationMismatch: .diagnostics.bundledeploymentsWithSyncGenerationMismatch,
            stuckBundleDeployments: .diagnostics.stuckBundleDeployments,
            orphanedSecrets: .orphanedSecrets,
            invalidSecretOwners: .diagnostics.invalidSecretOwners
        },
        commits: {
            gitrepos: [.gitrepos[] | {name, commit: .commit[0:8]}],
            bundles: [.bundles[] | select(.commit != null) | {name, commit: .commit[0:8], uid: .uid[0:13]}],
            bundledeployments: [.bundledeployments[] | select(.commit != null) | {name, commit: .commit[0:8], uid: .uid[0:13]}]
        }
    }'
}

# Main execution
case "$MODE" in
    --compare)
        if [ -z "$INPUT_FILE" ] || [ -z "$3" ]; then
            echo "Usage: $0 --compare <file1> <file2>"
            exit 1
        fi
        compare_snapshots "$INPUT_FILE" "$3"
        ;;
    --diff|-d)
        # Show differences between consecutive snapshots in a multiline file
        if [ -z "$INPUT_FILE" ]; then
            echo "Usage: $0 --diff <multiline-json-file>"
            exit 1
        fi

        # Read all snapshots into an array (portable version)
        snapshots=()
        while IFS= read -r line; do
            snapshots+=("$line")
        done < "$INPUT_FILE"
        total=${#snapshots[@]}

        if [ "$total" -lt 2 ]; then
            echo "Need at least 2 snapshots to show differences"
            exit 1
        fi

        print_header "Analyzing $total snapshots from $INPUT_FILE"

        # Show first snapshot summary
        echo "${snapshots[0]}" | summary_analysis

        # Compare consecutive snapshots
        for ((i=1; i<total; i++)); do
            echo ""
            print_header "Changes: Snapshot $i → $((i+1))"
            jq -n --argjson before "${snapshots[$((i-1))]}" --argjson after "${snapshots[$i]}" '
                $before as $b | $after as $a |
                "Time: \($b.timestamp) → \($a.timestamp)",
                "",
                "RESOURCE COUNT CHANGES:",
                "  GitRepos: \($b.gitrepos | length) → \($a.gitrepos | length) \(if ($a.gitrepos | length) != ($b.gitrepos | length) then "⚠" else "" end)",
                "  Bundles: \($b.bundles | length) → \($a.bundles | length) \(if ($a.bundles | length) != ($b.bundles | length) then "⚠" else "" end)",
                "  BundleDeployments: \($b.bundledeployments | length) → \($a.bundledeployments | length) \(if ($a.bundledeployments | length) != ($b.bundledeployments | length) then "⚠" else "" end)",
                "  Contents: \($b.contents | length) → \($a.contents | length) \(if ($a.contents | length) != ($b.contents | length) then "⚠" else "" end)",
                "  Clusters: \($b.clusters | length) → \($a.clusters | length) \(if ($a.clusters | length) != ($b.clusters | length) then "⚠" else "" end)",
                "",
                "DIAGNOSTIC CHANGES:",
                "  Bundles With Generation Mismatch: \($b.diagnostics.bundlesWithGenerationMismatch | length) → \($a.diagnostics.bundlesWithGenerationMismatch | length) \(if ($a.diagnostics.bundlesWithGenerationMismatch | length) > ($b.diagnostics.bundlesWithGenerationMismatch | length) then "⚠ INCREASED" elif ($a.diagnostics.bundlesWithGenerationMismatch | length) < ($b.diagnostics.bundlesWithGenerationMismatch | length) then "✓ DECREASED" else "" end)",
                "  BundleDeployments With SyncGeneration Mismatch: \($b.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) → \($a.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) \(if ($a.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) > ($b.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) then "⚠ INCREASED" elif ($a.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) < ($b.diagnostics.bundledeploymentsWithSyncGenerationMismatch | length) then "✓ DECREASED" else "" end)",
                "  Stuck BundleDeployments: \($b.diagnostics.stuckBundleDeployments | length) → \($a.diagnostics.stuckBundleDeployments | length) \(if ($a.diagnostics.stuckBundleDeployments | length) > ($b.diagnostics.stuckBundleDeployments | length) then "⚠ INCREASED" elif ($a.diagnostics.stuckBundleDeployments | length) < ($b.diagnostics.stuckBundleDeployments | length) then "✓ DECREASED" else "" end)",
                "  Large Bundles: \($b.diagnostics.largeBundles | length) → \($a.diagnostics.largeBundles | length) \(if ($a.diagnostics.largeBundles | length) > ($b.diagnostics.largeBundles | length) then "⚠ INCREASED" elif ($a.diagnostics.largeBundles | length) < ($b.diagnostics.largeBundles | length) then "✓ DECREASED" else "" end)",
                "  Agent Issues: \($b.diagnostics.clustersWithAgentIssues | length) → \($a.diagnostics.clustersWithAgentIssues | length) \(if ($a.diagnostics.clustersWithAgentIssues | length) > ($b.diagnostics.clustersWithAgentIssues | length) then "⚠ INCREASED" elif ($a.diagnostics.clustersWithAgentIssues | length) < ($b.diagnostics.clustersWithAgentIssues | length) then "✓ DECREASED" else "" end)",
                "",
                "BUNDLE SIZE CHANGES:",
                (
                    ($a.bundles // []) as $ab |
                    ($b.bundles // []) as $bb |
                    ($ab | map({(.name): .sizeBytes}) | add // {}) as $asizes |
                    ($bb | map({(.name): .sizeBytes}) | add // {}) as $bsizes |
                    [
                        $asizes | to_entries[] |
                        select(.value != $bsizes[.key]) |
                        "  \(.key): \(($bsizes[.key] // 0) / 1024 | floor)KB → \(.value / 1024 | floor)KB \(if .value > ($bsizes[.key] // 0) then "⚠ GREW" else "✓ SHRUNK" end)"
                    ] | if length > 0 then .[] else "  No size changes" end
                ),
                ""
            '
        done

        # Show final summary
        echo ""
        print_header "Final Snapshot Summary"
        echo "${snapshots[$((total-1))]}" | summary_analysis
        ;;
    --live)
        # Monitor a file that's being appended to
        if [ -z "$INPUT_FILE" ]; then
            echo "Usage: $0 --live <multiline-json-file>"
            exit 1
        fi

        print_info "Monitoring $INPUT_FILE for new snapshots..."

        prev_snapshot=""
        tail -f "$INPUT_FILE" | while IFS= read -r line; do
            echo ""
            echo "═══════════════════════════════════════════════════════════"
            echo "$line" | summary_analysis

            if [ -n "$prev_snapshot" ]; then
                echo ""
                print_header "Changes from Previous Snapshot"
                jq -n --argjson before "$prev_snapshot" --argjson after "$line" '
                    $before as $b | $after as $a |
                    "Resources: GR:\($b.gitrepos|length)→\($a.gitrepos|length) B:\($b.bundles|length)→\($a.bundles|length) BD:\($b.bundledeployments|length)→\($a.bundledeployments|length)",
                    "GenMismatch: B:\($b.diagnostics.bundlesWithGenerationMismatch|length)→\($a.diagnostics.bundlesWithGenerationMismatch|length) Stuck BD:\($b.diagnostics.stuckBundleDeployments|length)→\($a.diagnostics.stuckBundleDeployments|length)"
                '
            fi

            prev_snapshot="$line"
        done
        ;;
    --all)
        # Show summary of all snapshots in file
        if [ -z "$INPUT_FILE" ]; then
            echo "Usage: $0 --all <multiline-json-file>"
            exit 1
        fi

        # Read all snapshots into an array (portable version)
        snapshots=()
        while IFS= read -r line; do
            snapshots+=("$line")
        done < "$INPUT_FILE"
        total=${#snapshots[@]}

        print_header "Analyzing $total snapshots from $INPUT_FILE"

        for ((i=0; i<total; i++)); do
            echo ""
            print_info "Snapshot $((i+1))/$total"
            echo "${snapshots[$i]}" | summary_analysis
            echo "────────────────────────────────────────────────────────────"
        done
        ;;
    --json)
        if [ -n "$INPUT_FILE" ]; then
            cat "$INPUT_FILE" | json_output
        else
            json_output
        fi
        ;;
    --detailed)
        if [ -n "$INPUT_FILE" ]; then
            cat "$INPUT_FILE" | detailed_analysis
        else
            detailed_analysis
        fi
        ;;
    --issues|-i)
        if [ -n "$INPUT_FILE" ]; then
            cat "$INPUT_FILE" | issues_only
        else
            issues_only
        fi
        ;;
    --summary|-s|summary|*)
        if [ -n "$INPUT_FILE" ]; then
            # Check if file has multiple lines (snapshots)
            line_count=$(wc -l < "$INPUT_FILE" | tr -d ' ')
            if [ "$line_count" -gt 1 ]; then
                print_info "File contains $line_count snapshots. Showing latest snapshot only."
                print_info "Use --diff to see changes between snapshots, or --all to see all summaries."
                echo ""
                tail -1 "$INPUT_FILE" | summary_analysis
            else
                cat "$INPUT_FILE" | summary_analysis
            fi
        else
            summary_analysis
        fi
        ;;
esac
