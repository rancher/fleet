package troubleshooting

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
)

// ReadSnapshots reads newline-delimited JSON snapshots from an io.Reader.
func ReadSnapshots(input io.Reader) ([]*Snapshot, error) {
	var snapshots []*Snapshot
	scanner := bufio.NewScanner(input)

	// Increase buffer size for large snapshots
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024) // 10MB max

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var snapshot Snapshot
		if err := json.Unmarshal(line, &snapshot); err != nil {
			return nil, fmt.Errorf("failed to parse snapshot: %w", err)
		}
		snapshots = append(snapshots, &snapshot)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	return snapshots, nil
}

// OutputSummary writes a summary of the given snapshot to w.
func OutputSummary(w io.Writer, snapshot *Snapshot) error {
	printHeader(w, "FLEET MONITORING SUMMARY - "+snapshot.Timestamp)

	fmt.Fprintln(w)
	printSubHeader(w, "RESOURCE COUNTS")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  GitRepos:\t%d\n", len(snapshot.GitRepos))

	totalSize := int64(0)
	for _, b := range snapshot.Bundles {
		if b.SizeBytes != nil {
			totalSize += *b.SizeBytes
		}
	}
	fmt.Fprintf(tw, "  Bundles:\t%d\t(Total Size: %dKB)\n", len(snapshot.Bundles), totalSize/1024)
	fmt.Fprintf(tw, "  BundleDeployments:\t%d\n", len(snapshot.BundleDeployments))
	fmt.Fprintf(tw, "  Contents:\t%d\n", len(snapshot.Contents))
	fmt.Fprintf(tw, "  Clusters:\t%d\n", len(snapshot.Clusters))
	fmt.Fprintf(tw, "  ClusterGroups:\t%d\n", len(snapshot.ClusterGroups))
	tw.Flush()

	if snapshot.Diagnostics != nil {
		fmt.Fprintln(w)
		printSubHeader(w, "DIAGNOSTICS SUMMARY")
		printDiagnosticsSummary(w, snapshot.Diagnostics)
	}

	return nil
}

// OutputAll writes a summary of all snapshots to w.
func OutputAll(w io.Writer, snapshots []*Snapshot) error {
	printHeader(w, fmt.Sprintf("Analyzing %d snapshots", len(snapshots)))

	for i, snapshot := range snapshots {
		fmt.Fprintf(w, "\n")
		printInfo(w, fmt.Sprintf("Snapshot %d/%d", i+1, len(snapshots)))
		if err := OutputSummary(w, snapshot); err != nil {
			return err
		}
		fmt.Fprintln(w, strings.Repeat("─", 60))
	}

	return nil
}

// OutputDiff writes changes between consecutive snapshots to w.
func OutputDiff(w io.Writer, snapshots []*Snapshot) error {
	if len(snapshots) < 2 {
		return fmt.Errorf("need at least 2 snapshots to show diff")
	}

	printHeader(w, fmt.Sprintf("Changes Across %d Snapshots", len(snapshots)))

	for i := 1; i < len(snapshots); i++ {
		before := snapshots[i-1]
		after := snapshots[i]

		fmt.Fprintf(w, "\n")
		printSubHeader(w, fmt.Sprintf("Snapshot %d → %d", i, i+1))
		fmt.Fprintf(w, "Time: %s → %s\n", before.Timestamp, after.Timestamp)

		PrintSnapshotDiff(w, before, after)
		fmt.Fprintln(w, strings.Repeat("─", 60))
	}

	// Show final summary
	fmt.Fprintf(w, "\n")
	printHeader(w, "Final Snapshot Summary")
	return OutputSummary(w, snapshots[len(snapshots)-1])
}

// OutputIssues writes only resources with detected issues to w.
//
//nolint:gocyclo
func OutputIssues(w io.Writer, snapshots []*Snapshot) error {
	snapshot := snapshots[len(snapshots)-1]

	printHeader(w, "ISSUES DETECTED - "+snapshot.Timestamp)

	if snapshot.Diagnostics == nil {
		printSuccess(w, "No diagnostics available")
		return nil
	}

	diag := snapshot.Diagnostics
	hasIssues := false

	// Stuck BundleDeployments
	if len(diag.StuckBundleDeployments) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printError(w, fmt.Sprintf("Stuck BundleDeployments (%d)", len(diag.StuckBundleDeployments)))
		for _, bd := range diag.StuckBundleDeployments {
			fmt.Fprintf(w, "  • %s/%s\n", bd.Namespace, bd.Name)
			if bd.DeploymentID != bd.AppliedDeploymentID {
				fmt.Fprintf(w, "    DeploymentID: %s != AppliedDeploymentID: %s\n",
					truncate(bd.DeploymentID, 40), truncate(bd.AppliedDeploymentID, 40))
			}
			if bd.ForceSyncGeneration > 0 && bd.SyncGeneration != nil && *bd.SyncGeneration != bd.ForceSyncGeneration {
				fmt.Fprintf(w, "    SyncGeneration: %d != ForceSyncGeneration: %d\n",
					*bd.SyncGeneration, bd.ForceSyncGeneration)
			}
		}
	}

	// GitRepo/Bundle inconsistencies
	if len(diag.GitRepoBundleInconsistencies) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printError(w, fmt.Sprintf("GitRepo/Bundle Inconsistencies (%d)", len(diag.GitRepoBundleInconsistencies)))
		for _, b := range diag.GitRepoBundleInconsistencies {
			fmt.Fprintf(w, "  • %s/%s\n", b.Namespace, b.Name)
			if b.Commit != "" {
				fmt.Fprintf(w, "    Commit: %s\n", truncate(b.Commit, 40))
			}
			if b.ForceSyncGeneration != 0 {
				fmt.Fprintf(w, "    ForceSyncGeneration: %d\n", b.ForceSyncGeneration)
			}
		}
	}

	// Target matching issues
	if len(diag.BundlesWithNoDeployments) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("Bundles with No Deployments (%d)", len(diag.BundlesWithNoDeployments)))
		for _, b := range diag.BundlesWithNoDeployments {
			fmt.Fprintf(w, "  • %s/%s\n", b.Namespace, b.Name)
		}
	}

	if len(diag.GitReposWithNoBundles) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("GitRepos with No Bundles (%d)", len(diag.GitReposWithNoBundles)))
		for _, gr := range diag.GitReposWithNoBundles {
			fmt.Fprintf(w, "  • %s/%s\n", gr.Namespace, gr.Name)
		}
	}

	// Agent issues
	if len(diag.ClustersWithAgentIssues) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printError(w, fmt.Sprintf("Clusters with Agent Issues (%d)", len(diag.ClustersWithAgentIssues)))
		for _, c := range diag.ClustersWithAgentIssues {
			fmt.Fprintf(w, "  • %s/%s", c.Namespace, c.Name)
			if !c.Ready {
				fmt.Fprintf(w, " (Not Ready)")
			}
			if c.AgentLastSeenAge != "" {
				fmt.Fprintf(w, " (Last seen: %s ago)", c.AgentLastSeenAge)
			}
			fmt.Fprintln(w)
		}
	}

	// Large bundles
	if len(diag.LargeBundles) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("Large Bundles (>1MB) (%d)", len(diag.LargeBundles)))
		for _, b := range diag.LargeBundles {
			size := int64(0)
			if b.SizeBytes != nil {
				size = *b.SizeBytes
			}
			fmt.Fprintf(w, "  • %s/%s (%dKB)\n", b.Namespace, b.Name, size/1024)
		}
	}

	// Unpolled GitRepos
	if len(diag.GitReposUnpolled) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("GitRepos last polled too long ago (%d)", len(diag.GitReposUnpolled)))
		for _, gr := range diag.GitReposUnpolled {
			fmt.Fprintf(w, "  • %s/%s (last polled: %s, polling interval: %s)\n",
				gr.Namespace, gr.Name, gr.LastPollingTime, gr.PollingInterval)
		}
	}

	// Commit mismatches
	if len(diag.GitReposWithCommitMismatch) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("GitRepos with Commit Mismatch (%d)", len(diag.GitReposWithCommitMismatch)))
		for _, gr := range diag.GitReposWithCommitMismatch {
			fmt.Fprintf(w, "  • %s/%s (polling: %s, webhook: %s, status commit: %s)\n",
				gr.Namespace, gr.Name, gr.WebhookCommit, gr.PollingCommit, gr.Commit)
		}
	}

	// Generation mismatches
	if len(diag.GitReposWithGenerationMismatch) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("GitRepos with Generation Mismatch (%d)", len(diag.GitReposWithGenerationMismatch)))
		for _, gr := range diag.GitReposWithGenerationMismatch {
			fmt.Fprintf(w, "  • %s/%s (gen: %d, observed: %d)\n",
				gr.Namespace, gr.Name, gr.Generation, gr.ObservedGeneration)
		}
	}

	if len(diag.BundlesWithGenerationMismatch) > 0 {
		hasIssues = true
		fmt.Fprintln(w)
		printWarning(w, fmt.Sprintf("Bundles with Generation Mismatch (%d)", len(diag.BundlesWithGenerationMismatch)))
		for _, b := range diag.BundlesWithGenerationMismatch {
			fmt.Fprintf(w, "  • %s/%s (gen: %d, observed: %d)\n",
				b.Namespace, b.Name, b.Generation, b.ObservedGeneration)
		}
	}

	if !hasIssues {
		printSuccess(w, "No issues detected!")
	}

	return nil
}

// OutputDetailed writes a detailed analysis of the latest snapshot to w.
func OutputDetailed(w io.Writer, snapshots []*Snapshot) error {
	snapshot := snapshots[len(snapshots)-1]

	// First show summary
	if err := OutputSummary(w, snapshot); err != nil {
		return err
	}

	// Then show issues
	if err := OutputIssues(w, snapshots); err != nil {
		return err
	}

	// Show controller info
	if len(snapshot.Controller) > 0 {
		fmt.Fprintln(w)
		printSubHeader(w, "CONTROLLER INFO")
	}
	for _, c := range snapshot.Controller {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "  Name:\t%s\n", c.Name)
		fmt.Fprintf(tw, "  Status:\t%s\n", c.Status)
		fmt.Fprintf(tw, "  Restarts:\t%d\n", c.Restarts)
		if c.StartTime != "" {
			fmt.Fprintf(tw, "  Start Time:\t%s\n", c.StartTime)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	// Show API consistency
	if snapshot.APIConsistency != nil {
		fmt.Fprintln(w)
		printSubHeader(w, "API CONSISTENCY")
		if snapshot.APIConsistency.Consistent {
			printSuccess(w, "API is consistent")
		} else {
			printError(w, "API inconsistency detected (time travel)")
			fmt.Fprintf(w, "  Versions seen: %v\n", snapshot.APIConsistency.Versions)
		}
	}

	// Show recent events if there are warning/error events
	if len(snapshot.RecentEvents) > 0 {
		warningEvents := 0
		for _, e := range snapshot.RecentEvents {
			if e.Type == "Warning" {
				warningEvents++
			}
		}

		if warningEvents > 0 {
			fmt.Fprintln(w)
			printSubHeader(w, fmt.Sprintf("RECENT WARNING EVENTS (%d)", warningEvents))
			for _, e := range snapshot.RecentEvents {
				if e.Type == "Warning" {
					fmt.Fprintf(w, "  • [%s] %s/%s: %s\n", e.Reason, e.InvolvedKind, e.InvolvedName, e.Message)
					if e.LastTimestamp != "" {
						fmt.Fprintf(w, "    Last: %s", e.LastTimestamp)
						if e.Count > 1 {
							fmt.Fprintf(w, " (x%d)", e.Count)
						}
						fmt.Fprintln(w)
					}
				}
			}
		}
	}

	return nil
}

// OutputJSON writes a machine-readable JSON representation of the snapshots to w.
// If showAll is true, all snapshots are included in the output; otherwise only the latest is shown.
func OutputJSON(w io.Writer, snapshots []*Snapshot, showAll bool) error {
	type Output struct {
		SnapshotCount int         `json:"snapshotCount"`
		Latest        *Snapshot   `json:"latest"`
		All           []*Snapshot `json:"all,omitempty"`
	}

	output := Output{
		SnapshotCount: len(snapshots),
		Latest:        snapshots[len(snapshots)-1],
	}

	if showAll {
		output.All = snapshots
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

// PrintSnapshotDiff writes a diff between two snapshots to w.
func PrintSnapshotDiff(w io.Writer, before, after *Snapshot) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "\nRESOURCE COUNTS:")
	printCountChange(tw, "GitRepos", len(before.GitRepos), len(after.GitRepos))
	printCountChange(tw, "Bundles", len(before.Bundles), len(after.Bundles))
	printCountChange(tw, "BundleDeployments", len(before.BundleDeployments), len(after.BundleDeployments))
	printCountChange(tw, "Contents", len(before.Contents), len(after.Contents))
	printCountChange(tw, "Clusters", len(before.Clusters), len(after.Clusters))

	if before.Diagnostics != nil && after.Diagnostics != nil {
		fmt.Fprintln(tw, "\nDIAGNOSTICS CHANGES:")
		printCountChange(tw, "Stuck BundleDeployments",
			len(before.Diagnostics.StuckBundleDeployments),
			len(after.Diagnostics.StuckBundleDeployments))
		printCountChange(tw, "GitRepo/Bundle Mismatches",
			len(before.Diagnostics.GitRepoBundleInconsistencies),
			len(after.Diagnostics.GitRepoBundleInconsistencies))
		printCountChange(tw, "Bundles with No Deployments",
			len(before.Diagnostics.BundlesWithNoDeployments),
			len(after.Diagnostics.BundlesWithNoDeployments))
		printCountChange(tw, "Agent Issues",
			len(before.Diagnostics.ClustersWithAgentIssues),
			len(after.Diagnostics.ClustersWithAgentIssues))
	}

	tw.Flush()

	// Show bundle size changes
	printBundleSizeChanges(w, before, after)
}

// PrintHeader writes a formatted section header to w.
func PrintHeader(w io.Writer, text string) {
	printHeader(w, text)
}

func printDiagnosticsSummary(w io.Writer, diag *Diagnostics) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Stuck Resources
	fmt.Fprintln(tw, "  Stuck Resources:")
	printDiagLine(tw, "Stuck BundleDeployments", len(diag.StuckBundleDeployments))

	// Inconsistencies
	inconsistencies := len(diag.GitRepoBundleInconsistencies) + diag.ContentIssuesCount
	fmt.Fprintf(tw, "  Inconsistencies:\t%d\t%s\n", inconsistencies, statusIcon(inconsistencies))
	printDiagLine(tw, "GitRepo/Bundle Mismatches", len(diag.GitRepoBundleInconsistencies))
	printDiagLine(tw, "Content Issues", diag.ContentIssuesCount)

	// Target Matching
	targetIssues := len(diag.BundlesWithNoDeployments) + len(diag.GitReposWithNoBundles) + len(diag.ClusterGroupsWithNoClusters)
	fmt.Fprintf(tw, "  Target Matching:\t%d\t%s\n", targetIssues, statusIcon(targetIssues))
	printDiagLine(tw, "Bundles with No Deployments", len(diag.BundlesWithNoDeployments))
	printDiagLine(tw, "GitRepos with No Bundles", len(diag.GitReposWithNoBundles))
	printDiagLine(tw, "ClusterGroups with No Clusters", len(diag.ClusterGroupsWithNoClusters))

	// Ownership Issues
	ownershipIssues := len(diag.BundlesWithMissingGitRepo) + len(diag.BundleDeploymentsWithMissingBundle) + len(diag.ResourcesWithMultipleFinalizers)
	fmt.Fprintf(tw, "  Ownership Issues:\t%d\t%s\n", ownershipIssues, statusIcon(ownershipIssues))
	printDiagLine(tw, "Bundles with Missing GitRepo", len(diag.BundlesWithMissingGitRepo))
	printDiagLine(tw, "BundleDeployments with Missing Bundle", len(diag.BundleDeploymentsWithMissingBundle))
	printDiagLine(tw, "Resources with Multiple Finalizers", len(diag.ResourcesWithMultipleFinalizers))

	// Agent Issues
	fmt.Fprintf(tw, "  Agent Issues:\t%d\t%s\n", len(diag.ClustersWithAgentIssues), statusIcon(len(diag.ClustersWithAgentIssues)))

	// Performance Issues
	perfIssues := len(diag.LargeBundles) + len(diag.BundlesWithMissingContent)
	fmt.Fprintf(tw, "  Performance Issues:\t%d\t%s\n", perfIssues, statusIcon(perfIssues))
	printDiagLine(tw, "Large Bundles (>1MB)", len(diag.LargeBundles))
	printDiagLine(tw, "Bundles with Missing Content", len(diag.BundlesWithMissingContent))

	// Generation Mismatches
	genIssues := len(diag.GitReposWithGenerationMismatch) + len(diag.BundlesWithGenerationMismatch) + len(diag.BundleDeploymentsWithSyncGenerationMismatch)
	fmt.Fprintf(tw, "  Generation Mismatches:\t%d\t%s\n", genIssues, statusIcon(genIssues))
	printDiagLine(tw, "GitRepos", len(diag.GitReposWithGenerationMismatch))
	printDiagLine(tw, "Bundles", len(diag.BundlesWithGenerationMismatch))
	printDiagLine(tw, "BundleDeployments (SyncGen)", len(diag.BundleDeploymentsWithSyncGenerationMismatch))

	// Commit Mismatches
	commitIssues := len(diag.GitReposWithCommitMismatch)
	fmt.Fprintf(tw, "  Commit Mismatches:\t%d\t%s\n", commitIssues, statusIcon(commitIssues))

	// GitRepos last polled too long ago
	pollingIssues := len(diag.GitReposUnpolled)
	fmt.Fprintf(tw, "  GitRepos last polled too long ago:\t%d\t%s\n", pollingIssues, statusIcon(pollingIssues))

	// Deletion Timestamps
	if diag.BundlesWithDeletionTimestamp > 0 || diag.BundleDeploymentsWithDeletionTimestamp > 0 || diag.ContentsWithDeletionTimestamp > 0 {
		fmt.Fprintln(tw, "  Deletion Timestamps:")
		if diag.BundlesWithDeletionTimestamp > 0 {
			printDiagLine(tw, "Bundles", diag.BundlesWithDeletionTimestamp)
		}
		if diag.BundleDeploymentsWithDeletionTimestamp > 0 {
			printDiagLine(tw, "BundleDeployments", diag.BundleDeploymentsWithDeletionTimestamp)
		}
		if diag.ContentsWithDeletionTimestamp > 0 {
			printDiagLine(tw, "Contents", diag.ContentsWithDeletionTimestamp)
		}
	}

	if diag.ContentsWithZeroReferenceCount > 0 {
		fmt.Fprintln(tw, "  Reference counts:")
		printDiagLine(tw, "Contents with 0 reference count", diag.ContentsWithZeroReferenceCount)
	}

	tw.Flush()
}

func printBundleSizeChanges(w io.Writer, before, after *Snapshot) {
	beforeSizes := make(map[string]int64)
	for _, b := range before.Bundles {
		if b.SizeBytes != nil {
			beforeSizes[b.Namespace+"/"+b.Name] = *b.SizeBytes
		}
	}

	afterSizes := make(map[string]int64)
	for _, b := range after.Bundles {
		if b.SizeBytes != nil {
			afterSizes[b.Namespace+"/"+b.Name] = *b.SizeBytes
		}
	}

	var changes []string
	for name, afterSize := range afterSizes {
		beforeSize := beforeSizes[name]
		if beforeSize != afterSize && beforeSize != 0 {
			change := fmt.Sprintf("  %s: %dKB → %dKB", name, beforeSize/1024, afterSize/1024)
			if afterSize > beforeSize {
				change += " " + color.YellowString("⚠ GREW")
			} else {
				change += " " + color.GreenString("✓ SHRUNK")
			}
			changes = append(changes, change)
		}
	}

	if len(changes) > 0 {
		fmt.Fprintln(w, "\nBUNDLE SIZE CHANGES:")
		for _, change := range changes {
			fmt.Fprintln(w, change)
		}
	}
}

// Helper functions for formatting

func printHeader(w io.Writer, text string) {
	fmt.Fprintln(w, color.New(color.Bold, color.FgBlue).Sprintf("\n=== %s ===\n", text))
}

func printSubHeader(w io.Writer, text string) {
	fmt.Fprintln(w, color.New(color.Bold).Sprint(text))
}

func printError(w io.Writer, text string) {
	fmt.Fprintln(w, color.RedString("✗ %s", text))
}

func printWarning(w io.Writer, text string) {
	fmt.Fprintln(w, color.YellowString("⚠ %s", text))
}

func printSuccess(w io.Writer, text string) {
	fmt.Fprintln(w, color.GreenString("✓ %s", text))
}

func printInfo(w io.Writer, text string) {
	fmt.Fprintln(w, color.CyanString("ℹ %s", text))
}

func printDiagLine(tw *tabwriter.Writer, label string, count int) {
	fmt.Fprintf(tw, "    - %s:\t%d\n", label, count)
}

func printCountChange(tw *tabwriter.Writer, label string, before, after int) {
	change := after - before
	icon := "→"
	if change > 0 {
		icon = color.YellowString("↑")
	} else if change < 0 {
		icon = color.GreenString("↓")
	}
	fmt.Fprintf(tw, "  %s:\t%d → %d\t%s\n", label, before, after, icon) //nolint:gosec // G705 false positive: tw wraps a CLI stdout writer, not an HTTP ResponseWriter
}

func statusIcon(count int) string {
	if count == 0 {
		return color.GreenString("✓")
	}
	return color.YellowString("⚠")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
