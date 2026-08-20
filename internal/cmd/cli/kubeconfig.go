package cli

import (
	"flag"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

// registerKubeconfigFlags adds the controller-runtime flags, --kubeconfig
// among them, to cmd.
func registerKubeconfigFlags(cmd *cobra.Command) {
	addGoFlags(cmd, flag.NewFlagSet("", flag.ExitOnError))
}

// registerLoggingAndKubeconfigFlags does the same as registerKubeconfigFlags,
// and also exposes the zap logging flags, for the commands which set up the
// controller-runtime logger from them.
func registerLoggingAndKubeconfigFlags(cmd *cobra.Command) {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	addGoFlags(cmd, fs)
}

// addGoFlags converts fs, together with the controller-runtime flags, to
// pflags and adds them to cmd. Every subcommand which talks to a cluster has
// to go through here, so that --kubeconfig reaches the loader used by
// getKubeconfig.
//
// ctrl.RegisterFlags binds --kubeconfig via the stdlib flag package, which has
// no notion of shorthands. pflag only records a shorthand while adding a flag
// to a set, in AddFlag, so "k" has to be set on the way in: assigning
// Shorthand afterwards updates the usage output but leaves -k unparsable.
func addGoFlags(cmd *cobra.Command, fs *flag.FlagSet) {
	ctrl.RegisterFlags(fs)

	pfs := pflag.NewFlagSet("", pflag.ExitOnError)
	pfs.AddGoFlagSet(fs)
	if f := pfs.Lookup("kubeconfig"); f != nil {
		f.Shorthand = "k"
	}

	cmd.Flags().AddFlagSet(pfs)
}

// getKubeconfig returns the rest config to use, looked up from --kubeconfig,
// the KUBECONFIG environment variable, the in-cluster config or
// ~/.kube/config, in that order. Unlike ctrl.GetConfigOrDie it lets the caller
// report the failure instead of exiting the process.
func getKubeconfig() (*rest.Config, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	return cfg, nil
}
