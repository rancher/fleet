package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	command "github.com/rancher/fleet/internal/cmd"
)

// Analyze provides analysis and visualization of Fleet monitor snapshots.
//
// # Overview
//
// The analyze command processes JSON output from the monitor command to provide
// human-readable analysis, detect changes over time, and identify issues.
//
// # Usage Modes
//
// Single Snapshot Analysis (default):
//
//	fleet monitor | fleet analyze
//	fleet analyze snapshot.json
//
// Multiple Snapshots (shows latest):
//
//	fleet analyze monitor.json
//
// Show All Snapshots:
//
//	fleet analyze --all monitor.json
//
// Diff Between Snapshots:
//
//	fleet analyze --diff monitor.json
//
// Show Only Issues:
//
//	fleet analyze --issues monitor.json
//
// Detailed Analysis:
//
//	fleet analyze --detailed monitor.json
//
// JSON Output (programmatic):
//
//	fleet analyze --json monitor.json
//
// Compare Two Snapshots:
//
//	fleet analyze --compare snapshot1.json snapshot2.json
//
// # Output Formats
//
// The analyze command provides several output modes:
//   - summary: High-level overview (default)
//   - all: Summary of all snapshots in file
//   - diff: Changes between consecutive snapshots
//   - issues: Only resources with problems
//   - detailed: Complete diagnostic information
//   - json: Machine-readable JSON output
type Analyze struct {
	All      bool   `usage:"Show summary of all snapshots in file"`
	Diff     bool   `usage:"Show changes between consecutive snapshots"`
	Issues   bool   `usage:"Show only resources with issues"`
	Detailed bool   `usage:"Show detailed analysis"`
	JSON     bool   `usage:"Output in JSON format"`
	Compare  string `usage:"Compare with another snapshot file"`
	NoColor  bool   `usage:"Disable colored output"`
}

func NewAnalyze() *cobra.Command {
	cmd := command.Command(&Analyze{}, cobra.Command{
		Use:   "analyze [file]",
		Short: "Analyze Fleet monitor snapshots",
		Long: `Analyze Fleet monitor snapshots for issues and changes.

This command processes JSON output from the monitor command to provide human-readable
analysis, detect changes over time, and identify issues with Fleet deployments.

Examples:
  # Analyze latest snapshot from file
  fleet analyze monitor.json

  # Show all snapshots
  fleet analyze --all monitor.json

  # Show changes between snapshots
  fleet analyze --diff monitor.json

  # Show only issues
  fleet analyze --issues monitor.json

  # Compare two specific snapshots
  fleet analyze --compare snapshot1.json snapshot2.json

  # Pipe from monitor command
  fleet monitor | fleet analyze`,
	})
	cmd.SetOut(os.Stdout)
	return cmd
}

func (a *Analyze) Run(cmd *cobra.Command, args []string) error {
	// Setup colors
	if a.NoColor {
		color.NoColor = true
	}

	// Determine input source
	var input io.Reader = os.Stdin
	var filename string

	if len(args) > 0 {
		filename = args[0]
		file, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()
		input = file
	}

	// Compare mode requires two files
	if a.Compare != "" {
		if len(args) == 0 {
			return fmt.Errorf("--compare requires two snapshot files")
		}
		return a.compareFiles(cmd, filename, a.Compare)
	}

	// Read snapshots from input
	snapshots, err := a.readSnapshots(input)
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		return fmt.Errorf("no snapshots found")
	}

	// Execute appropriate mode
	switch {
	case a.JSON:
		return a.outputJSON(cmd, snapshots)
	case a.All:
		return a.outputAll(cmd, snapshots)
	case a.Diff:
		return a.outputDiff(cmd, snapshots)
	case a.Issues:
		return a.outputIssues(cmd, snapshots)
	case a.Detailed:
		return a.outputDetailed(cmd, snapshots)
	default:
		return a.outputSummary(cmd, snapshots[len(snapshots)-1])
	}
}

func (a *Analyze) readSnapshots(input io.Reader) ([]*Snapshot, error) {
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

func (a *Analyze) outputSummary(cmd *cobra.Command, snapshot *Snapshot) error {
	w := cmd.OutOrStdout()

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
		a.printDiagnosticsSummary(w, snapshot.Diagnostics)
	}

	return nil
}

func (a *Analyze) printDiagnosticsSummary(w io.Writer, diag *Diagnostics) {
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

func (a *Analyze) outputAll(cmd *cobra.Command, snapshots []*Snapshot) error {
	w := cmd.OutOrStdout()

	printHeader(w, fmt.Sprintf("Analyzing %d snapshots", len(snapshots)))

	for i, snapshot := range snapshots {
		fmt.Fprintf(w, "\n")
		printInfo(w, fmt.Sprintf("Snapshot %d/%d", i+1, len(snapshots)))
		if err := a.outputSummary(cmd, snapshot); err != nil {
			return err
		}
		fmt.Fprintln(w, strings.Repeat("─", 60))
	}

	return nil
}

func (a *Analyze) outputDiff(cmd *cobra.Command, snapshots []*Snapshot) error {
	w := cmd.OutOrStdout()

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

		a.printSnapshotDiff(w, before, after)
		fmt.Fprintln(w, strings.Repeat("─", 60))
	}

	// Show final summary
	fmt.Fprintf(w, "\n")
	printHeader(w, "Final Snapshot Summary")
	return a.outputSummary(cmd, snapshots[len(snapshots)-1])
}

func (a *Analyze) printSnapshotDiff(w io.Writer, before, after *Snapshot) {
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
	a.printBundleSizeChanges(w, before, after)
}

func (a *Analyze) printBundleSizeChanges(w io.Writer, before, after *Snapshot) {
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

func (a *Analyze) outputIssues(cmd *cobra.Command, snapshots []*Snapshot) error {
	w := cmd.OutOrStdout()
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

func (a *Analyze) outputDetailed(cmd *cobra.Command, snapshots []*Snapshot) error {
	w := cmd.OutOrStdout()
	snapshot := snapshots[len(snapshots)-1]

	// First show summary
	if err := a.outputSummary(cmd, snapshot); err != nil {
		return err
	}

	// Then show issues
	if err := a.outputIssues(cmd, snapshots); err != nil {
		return err
	}

	// Show controller info
	if snapshot.Controller != nil {
		fmt.Fprintln(w)
		printSubHeader(w, "CONTROLLER INFO")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "  Name:\t%s\n", snapshot.Controller.Name)
		fmt.Fprintf(tw, "  Status:\t%s\n", snapshot.Controller.Status)
		fmt.Fprintf(tw, "  Restarts:\t%d\n", snapshot.Controller.Restarts)
		if snapshot.Controller.StartTime != "" {
			fmt.Fprintf(tw, "  Start Time:\t%s\n", snapshot.Controller.StartTime)
		}
		tw.Flush()
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

func (a *Analyze) outputJSON(cmd *cobra.Command, snapshots []*Snapshot) error {
	type Output struct {
		SnapshotCount int         `json:"snapshotCount"`
		Latest        *Snapshot   `json:"latest"`
		All           []*Snapshot `json:"all,omitempty"`
	}

	output := Output{
		SnapshotCount: len(snapshots),
		Latest:        snapshots[len(snapshots)-1],
	}

	if a.All {
		output.All = snapshots
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func (a *Analyze) compareFiles(cmd *cobra.Command, file1, file2 string) error {
	w := cmd.OutOrStdout()

	// Read first file
	f1, err := os.Open(file1)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", file1, err)
	}
	defer f1.Close()

	snapshots1, err := a.readSnapshots(f1)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file1, err)
	}
	if len(snapshots1) == 0 {
		return fmt.Errorf("no snapshots in %s", file1)
	}

	// Read second file
	f2, err := os.Open(file2)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", file2, err)
	}
	defer f2.Close()

	snapshots2, err := a.readSnapshots(f2)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file2, err)
	}
	if len(snapshots2) == 0 {
		return fmt.Errorf("no snapshots in %s", file2)
	}

	// Use last snapshot from each file
	before := snapshots1[len(snapshots1)-1]
	after := snapshots2[len(snapshots2)-1]

	printHeader(w, "COMPARING SNAPSHOTS")
	fmt.Fprintf(w, "Before: %s (%s)\n", file1, before.Timestamp)
	fmt.Fprintf(w, "After:  %s (%s)\n", file2, after.Timestamp)

	a.printSnapshotDiff(w, before, after)

	return nil
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
	fmt.Fprintf(tw, "  %s:\t%d → %d\t%s\n", label, before, after, icon)
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
