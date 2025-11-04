package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/doctor"
)

/*
Highest priority:
fleet doctor report
	* dump GitRepos, HelmOps, bundles, bundle deployments (option to include secrets?)
	* content resources
	* clusters and cluster groups
	* gitreporestrictions and bundlenamespacemappings
	* k8s events
	* metrics

Then:
fleet doctor check
	* errors in statuses
	* missing secrets (helm, git, options, OCI access)
	* controller logs
	* orphan resources (e.g. leftover content resources, with or without finalizers)
	* bundles not targeting any cluster
*/

// NewDoctor returns a subcommand to troubleshoot Fleet's state
func NewDoctor() *cobra.Command {
	return command.Command(&Doctor{}, cobra.Command{
		Use:   "doctor [flags]",
		Short: "Dump state of Fleet-managed resources into an archive",
	})
}

type Doctor struct {
	FleetClient
	DumpPath string `usage:"Destination path for the dump" short:"p"`
}

func (d *Doctor) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := d.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}

	return nil
}

func (d *Doctor) Run(cmd *cobra.Command, args []string) error {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get k8s config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "dump path: ", d.DumpPath)
	if d.DumpPath == "" {
		err := fmt.Errorf("no destination path specified for state dump. Exiting")

		return err
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	di, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to create dynamic Kubernetes client: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return doctor.CreateReport(ctx, di, c, d.DumpPath)
}
