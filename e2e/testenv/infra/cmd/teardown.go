package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/e2e/testenv"
)

// teardownCmd represents the teardown command
var teardownCmd = &cobra.Command{
	Use:   "teardown [--git-server=(true|false)|--helm-registry=(true|false)|--oci-registry=(true|false)]",
	Short: "Tear down an end-to-end test environment",
	Long: `This tears down the git server, Helm registry, OCI registry, either separately or together, and
	associated resources needed to run end-to-end tests.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Tearing down test environment...")

		env := testenv.New()
		k := env.Kubectl.Namespace(InfraNamespace)

		// Only act on specified components, unless none is specified in which case all are affected.
		if !withGitServer && !withHelmRegistry && !withOCIRegistry {
			withGitServer, withOCIRegistry, withHelmRegistry = true, true, true
		}

		_, _ = k.Delete("gitrepo", "helm")

		if withGitServer {
			_, _ = k.Delete("deployment", "git-server")
			_, _ = k.Delete("service", "git-service")
		}

		if withOCIRegistry {
			_, _ = k.Delete("configmap", "zot-config")
			_, _ = k.Delete("deployment", "zot")
			_, _ = k.Delete("service", "zot-service")
			_, _ = k.Delete("secret", "zot-htpasswd")
		}

		if withHelmRegistry {
			_, _ = k.Delete("deployment", "chartmuseum")
			_, _ = k.Delete("service", "chartmuseum-service")
		}

		if withHelmRegistry && withOCIRegistry {
			_, _ = k.Delete("secret", "helm-tls")
			_, _ = k.Namespace(env.Namespace).Delete("secret", "helm-secret")
		}

	},
}
