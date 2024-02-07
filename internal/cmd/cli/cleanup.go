package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/internal/client"
	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/cleanup"
)

// NewCleanup returns a subcommand to `cleanup` cluster registrations
func NewCleanUp() *cobra.Command {
	return command.Command(&CleanUp{}, cobra.Command{
		Use:   "cleanup [flags]",
		Short: "Clean up outdated cluster registrations",
	})
}

type CleanUp struct {
	FleetClient
	Min    string `usage:"Minimum delay between deletes (default: 10ms)" name:"min"`
	Max    string `usage:"Maximum delay between deletes (default: 5s)" name:"max"`
	Factor string `usage:"Factor to increase delay between deletes (default: 1.1)" name:"factor"`
}

func (r *CleanUp) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}
	Client = client.NewGetter(r.Kubeconfig, r.Context, r.Namespace)
	return nil
}

func (a *CleanUp) Run(cmd *cobra.Command, args []string) error {
	var err error
	min := 10 * time.Millisecond
	if a.Min != "" {
		min, err = time.ParseDuration(a.Min)
		if err != nil {
			return err
		}
		if min <= 0 {
			return errors.New("min cannot be zero or less")
		}
	}

	max := 3 * time.Second
	if a.Max != "" {
		max, err = time.ParseDuration(a.Max)
		if err != nil {
			return err
		}
		if max <= 0 {
			return errors.New("max cannot be zero or less")
		}
	}

	if max < min {
		return errors.New("max cannot be less than min")
	}

	factor := 1.05
	if a.Factor != "" {
		factor, err = strconv.ParseFloat(a.Factor, 64)
		if err != nil {
			return err
		}
		if factor <= 1 || math.IsInf(factor, 0) || math.IsNaN(factor) {
			return errors.New("factor must be greater than 1 and finite")
		}
	}

	opts := cleanup.Options{
		Min:    min,
		Max:    max,
		Factor: factor,
	}

	fmt.Printf("Cleaning up outdated cluster registrations: %#v\n", opts)

	return cleanup.ClusterRegistrations(cmd.Context(), Client, opts)
}
