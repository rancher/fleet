package cmds

import (
	"fmt"
	"os"
	"time"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewApply() *cobra.Command {
	return command.Command(&Apply{}, cobra.Command{
		Use:   "apply [flags] BUNDLE_NAME PATH...",
		Short: "Render a bundle into a Kubernetes resource and apply it in the Fleet Manager",
	})
}

type Apply struct {
	BundleInputArgs
	OutputArgsNoDefault
	Label           map[string]string `usage:"Labels to apply to created bundles" short:"l"`
	TargetsFile     string            `usage:"Addition source of targets and restrictions to be append"`
	Compress        bool              `usage:"Force all resources to be compress" short:"c"`
	ServiceAccount  string            `usage:"Service account to assign to bundle created" short:"a"`
	SyncBefore      string            `usage:"Force sync all deployments before this RFC3339 time"`
	TargetNamespace string            `usage:"Ensure this bundle goes to this target namespace"`
}

func toTime(syncBefore string) (*time.Time, error) {
	if syncBefore == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, syncBefore)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (a *Apply) Run(cmd *cobra.Command, args []string) error {
	syncBefore, err := toTime(a.SyncBefore)
	if err != nil {
		return err
	}

	labels := a.Label
	if commit := os.Getenv("COMMIT"); commit != "" {
		if labels == nil {
			labels = map[string]string{}
		}
		labels["fleet.cattle.io/commit"] = commit
	}

	name := ""
	opts := &apply.Options{
		BundleFile:      a.BundleFile,
		Output:          writer.NewDefaultNone(a.Output),
		Compress:        a.Compress,
		ServiceAccount:  a.ServiceAccount,
		Labels:          a.Label,
		TargetsFile:     a.TargetsFile,
		TargetNamespace: a.TargetNamespace,
		SyncBefore:      syncBefore,
	}

	if a.File == "-" {
		opts.BundleReader = os.Stdin
		if len(args) != 1 {
			return fmt.Errorf("the bundle name is required as the first argument")
		}
		name = args[0]
	} else if a.File != "" {
		f, err := os.Open(a.File)
		if err != nil {
			return err
		}
		defer f.Close()
		opts.BundleReader = f
		if len(args) != 1 {
			return fmt.Errorf("the bundle name is required as the first argument")
		}
		name = args[0]
	} else if len(args) < 1 {
		return fmt.Errorf("at least one arguments is required BUNDLE_NAME")
	} else {
		name = args[0]
		args = args[1:]
	}

	return apply.Apply(cmd.Context(), Client, name, args, opts)
}
