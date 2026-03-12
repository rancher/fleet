package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/troubleshooting"
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
	snapshots, err := troubleshooting.ReadSnapshots(input)
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		return fmt.Errorf("no snapshots found")
	}

	w := cmd.OutOrStdout()

	// Execute appropriate mode
	switch {
	case a.JSON:
		return troubleshooting.OutputJSON(w, snapshots, a.All)
	case a.All:
		return troubleshooting.OutputAll(w, snapshots)
	case a.Diff:
		return troubleshooting.OutputDiff(w, snapshots)
	case a.Issues:
		return troubleshooting.OutputIssues(w, snapshots)
	case a.Detailed:
		return troubleshooting.OutputDetailed(w, snapshots)
	default:
		return troubleshooting.OutputSummary(w, snapshots[len(snapshots)-1])
	}
}

func (a *Analyze) compareFiles(cmd *cobra.Command, file1, file2 string) error {
	w := cmd.OutOrStdout()

	// Read first file
	f1, err := os.Open(file1)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", file1, err)
	}
	defer f1.Close()

	snapshots1, err := troubleshooting.ReadSnapshots(f1)
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

	snapshots2, err := troubleshooting.ReadSnapshots(f2)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file2, err)
	}
	if len(snapshots2) == 0 {
		return fmt.Errorf("no snapshots in %s", file2)
	}

	// Use last snapshot from each file
	before := snapshots1[len(snapshots1)-1]
	after := snapshots2[len(snapshots2)-1]

	if before.Timestamp > after.Timestamp {
		// Swap files for timestamp consistency
		file1, file2 = file2, file1
		before, after = after, before
	}

	troubleshooting.PrintHeader(w, "COMPARING SNAPSHOTS")
	fmt.Fprintf(w, "Before: %s (%s)\n", file1, before.Timestamp) //nolint:gosec // G705 false positive: w is a CLI stdout writer, not an HTTP ResponseWriter
	fmt.Fprintf(w, "After:  %s (%s)\n", file2, after.Timestamp)  //nolint:gosec // G705 false positive: w is a CLI stdout writer, not an HTTP ResponseWriter

	troubleshooting.PrintSnapshotDiff(w, before, after)

	return nil
}
