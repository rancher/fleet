package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/dump"
)

/*
Later:
fleet dump check
	* errors in statuses
	* missing secrets (helm, git, options, OCI access)
	* controller logs
	* orphan resources (e.g. leftover content resources, with or without finalizers)
	* bundles not targeting any cluster
*/

// NewDump returns a subcommand to dump Fleet's state
func NewDump() *cobra.Command {
	return command.Command(&Dump{}, cobra.Command{
		Use:   "dump [flags]",
		Short: "Dump state of upstream Fleet-managed resources into an archive",
	})
}

type Dump struct {
	FleetClient
	DumpPath            string `usage:"Destination path for the dump" short:"p"`
	FetchLimit          int64  `usage:"Limit number of items per resource that are fetched at once (0 means no limit)" short:"l" default:"500"`
	WithSecrets         bool   `usage:"Include secrets with full data"`
	WithSecretsMetadata bool   `usage:"Include secrets with metadata only"`
	WithContent         bool   `usage:"Include Content resources with full data"`
	WithContentMetadata bool   `usage:"Include Content resources with metadata only"`
}

func (d *Dump) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := d.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}

	return nil
}

func (d *Dump) Run(cmd *cobra.Command, args []string) error {
	if d.WithSecrets && d.WithSecretsMetadata {
		return fmt.Errorf("--with-secrets and --with-secrets-metadata are mutually exclusive")
	}
	if d.WithContent && d.WithContentMetadata {
		return fmt.Errorf("--with-content and --with-content-metadata are mutually exclusive")
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get k8s config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "dump path: ", d.DumpPath)
	if d.DumpPath == "" {
		err := fmt.Errorf("no destination path specified for state dump. Exiting")

		return err
	}

	if d.FetchLimit < 0 {
		return fmt.Errorf("fetch limit must be non-negative, got %d", d.FetchLimit)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	opts := dump.Options{
		FetchLimit:          d.FetchLimit,
		WithSecrets:         d.WithSecrets,
		WithSecretsMetadata: d.WithSecretsMetadata,
		WithContent:         d.WithContent,
		WithContentMetadata: d.WithContentMetadata,
	}

	return dump.Create(ctx, cfg, d.DumpPath, opts)
}
