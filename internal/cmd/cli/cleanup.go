package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/cleanup"
)

// NewCleanup returns a subcommand to `cleanup` cluster registrations
func NewCleanUp() *cobra.Command {
	cleanup := command.Command(&Cleanup{}, cobra.Command{
		Short:         "Clean up outdated resources",
		SilenceUsage:  true,
		SilenceErrors: true,
	})
	cleanup.AddCommand(
		NewClusterRegistration(),
		NewGitjob(),
	)
	return cleanup
}

func NewClusterRegistration() *cobra.Command {
	return command.Command(&ClusterRegistration{}, cobra.Command{
		Use:           "clusterregistration [flags]",
		Short:         "Clean up outdated cluster registrations",
		SilenceUsage:  true,
		SilenceErrors: true,
	})
}

func NewGitjob() *cobra.Command {
	return command.Command(&Gitjob{}, cobra.Command{
		Use:           "gitjob [flags]",
		Short:         "Clean up outdated git jobs",
		SilenceUsage:  true,
		SilenceErrors: true,
	})
}

type Cleanup struct {
}

func (c *Cleanup) Run(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}

type ClusterRegistration struct {
	FleetClient
	Min    string `usage:"Minimum delay between deletes (default: 10ms)" name:"min"`
	Max    string `usage:"Maximum delay between deletes (default: 5s)" name:"max"`
	Factor string `usage:"Factor to increase delay between deletes (default: 1.1)" name:"factor"`
}

func (r *ClusterRegistration) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}
	return nil
}

func (a *ClusterRegistration) Run(cmd *cobra.Command, args []string) error {
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

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	cfg := ctrl.GetConfigOrDie()
	client, err := newClient(ctx, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Cleaning up outdated cluster registrations: %#v\n", opts)

	return cleanup.ClusterRegistrations(ctx, client, opts)
}

type Gitjob struct {
	FleetClient
	BatchSize int `usage:"Number of git jobs to retrieve at once" name:"batch-size" default:"5000"`
}

func (r *Gitjob) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to set up debug logging: %w", err)
	}
	return nil
}

func (r *Gitjob) Run(cmd *cobra.Command, args []string) error {
	bs := r.BatchSize
	if bs <= 1 {
		return errors.New("factor must be greater than 1")
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	cfg := ctrl.GetConfigOrDie()
	client, err := newClient(ctx, cfg)
	if err != nil {
		return err
	}

	fmt.Printf("Cleaning up outdated git jobs, batch size at %d\n", bs)

	return cleanup.GitJobs(ctx, client, bs)
}
