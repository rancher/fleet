package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/e2e/testenv"
)

// teardownCmd represents the teardown command
var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Tear down an end-to-end test environment",
	Long:  `This tears down the git server, Helm registry and associated resources needed to run end-to-end tests.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Tearing down test environment...")

		env := testenv.New()
		k := env.Kubectl.Namespace(env.Namespace)

		_, _ = k.Delete("gitrepo", "helm")
		_, _ = k.Delete("deployment", "git-server")
		_, _ = k.Delete("service", "git-service")

		_, _ = k.Delete("configmap", "zot-config")
		_, _ = k.Delete("deployment", "zot")
		_, _ = k.Delete("service", "zot-service")
		_, _ = k.Delete("secret", "zot-htpasswd")

		_, _ = k.Delete("deployment", "chartmuseum")
		_, _ = k.Delete("service", "chartmuseum-service")

		_, _ = k.Delete("secret", "helm-tls")
		_, _ = k.Delete("secret", "helm-secret")
	},
}

func init() {
	rootCmd.AddCommand(teardownCmd)
}
