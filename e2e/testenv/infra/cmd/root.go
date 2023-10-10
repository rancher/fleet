package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "testenv",
		Short: "root test env command",
		Long:  `This command should not be run directly.`,
	}
	withGitServer, withHelmRegistry, withChartMuseum bool
)

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(teardownCmd)

	rootCmd.PersistentFlags().BoolVarP(&withGitServer, "git-server", "g", false, "with git server")
	rootCmd.PersistentFlags().BoolVarP(&withHelmRegistry, "helm-registry", "r", false, "with Helm registry")
	rootCmd.PersistentFlags().BoolVarP(&withChartMuseum, "chart-museum", "c", false, "with ChartMuseum")
}
