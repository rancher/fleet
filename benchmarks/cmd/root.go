package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "report",
		Short: "report on a ginkgo json",
		Long:  `This is used to analyze benchmark results.`,
	}
	input     string
	db        string
	verbose   bool
	debug     bool
	stats     bool
	timeout   time.Duration
	namespace string
)

func main() {
	Execute()
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(csvCmd)
	rootCmd.AddCommand(jsonCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "", false, "Enable debug output")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	jsonCmd.Flags().StringVarP(&input, "input", "i", "report.json", "Input file")

	csvCmd.Flags().StringVarP(&input, "input", "i", "report.json", "Input file")

	reportCmd.Flags().StringVarP(&input, "input", "i", "report.json", "Input file")
	reportCmd.Flags().StringVarP(&db, "db", "d", "db/", "Path to json file database dir")
	reportCmd.Flags().BoolVarP(&stats, "stats", "s", false, "Show StdDev and ZScore")

	runCmd.Flags().DurationVarP(&timeout, "timeout", "t", 5*time.Minute, "Timeout for experiments")
	runCmd.Flags().StringVarP(&namespace, "namespace", "n", "fleet-local", "Run benchmarks against clusters in this namespace")
	runCmd.Flags().StringVarP(&db, "db", "d", "db/", "Path to json file database dir. Will be created if it does not exist")
}
