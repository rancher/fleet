package cmds

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/modules/cli/cleanup"
	command "github.com/rancher/wrangler-cli"
)

func NewCleanUp() *cobra.Command {
	cmd := command.Command(&CleanUp{}, cobra.Command{
		Use:   "cleanup [flags]",
		Short: "Clean up outdated cluster registrations",
	})
	command.AddDebug(cmd, &Debug)
	return cmd
}

type CleanUp struct {
	Min    string `usage:"Minimum delay between deletes (default: 10ms)" name:"min"`
	Max    string `usage:"Maximum delay between deletes (default: 5s)" name:"max"`
	Factor string `usage:"Factor to increase delay between deletes (default: 1.1)" name:"factor"`
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
			return errors.New("min cannot be zero or negative")
		}
	}

	max := 5 * time.Second
	if a.Max != "" {
		max, err = time.ParseDuration(a.Max)
		if err != nil {
			return err
		}
		if max <= 0 {
			return errors.New("max cannot be zero or negative")
		}
	}

	if max < min {
		return errors.New("max cannot be less than min")
	}

	factor := 1.1
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
