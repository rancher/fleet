// Package controller starts the fleet controller.
package controller

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/cleanup"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/version"
)

type FleetManager struct {
	command.DebugConfig
	Kubeconfig    string `usage:"Kubeconfig file"`
	Namespace     string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	DisableGitops bool   `usage:"disable gitops components" name:"disable-gitops"`
}

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	setupCpuPprof(cmd.Context())
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()
	if err := start(cmd.Context(), f.Namespace, f.Kubeconfig, f.DisableGitops); err != nil {
		return err
	}

	<-cmd.Context().Done()
	return nil
}

func (r *FleetManager) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}

	// if debug is enabled in controller, enable in agents too (unless otherwise specified)
	propagateDebug, _ := strconv.ParseBool(os.Getenv("FLEET_PROPAGATE_DEBUG_SETTINGS_TO_AGENTS"))
	if propagateDebug && r.Debug {
		agent.DebugEnabled = true
		agent.DebugLevel = r.DebugLevel
	}

	return nil
}

func App() *cobra.Command {
	cmd := command.Command(&FleetManager{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	cmd.AddCommand(cleanup.App())
	cmd.AddCommand(agentmanagement.App())

	return cmd
}

// setupCpuPprof starts a goroutine that captures a cpu pprof profile
// into FLEET_CPU_PPROF_DIR every FLEET_CPU_PPROF_PERIOD
func setupCpuPprof(ctx context.Context) {
	if dir, ok := os.LookupEnv("FLEET_CPU_PPROF_DIR"); ok {
		go func() {
			var pprofCpuFile *os.File

			period := durations.DefaultCpuPprofPeriod
			if customPeriod, err := time.ParseDuration(os.Getenv("FLEET_CPU_PPROF_PERIOD")); err == nil {
				period = customPeriod
			}
			wait.UntilWithContext(ctx, func(ctx context.Context) {
				stopCpuPprof(pprofCpuFile)
				pprofCpuFile = startCpuPprof(dir)
			}, period)
		}()
	}
}

// stopCpuPprof concludes a cpu pprof capture, if any is ongoing
func stopCpuPprof(f *os.File) {
	pprof.StopCPUProfile()
	if f != nil {
		err := f.Close()
		if err != nil {
			log.Println("could not close CPU profile file ", err)
		}
	}
}

// startCpuPprof starts a pprof cpu capture into a timestamp-prefixed file in dir
func startCpuPprof(dir string) *os.File {
	name := time.Now().UTC().Format("2006-01-02_15_04_05") + ".pprof.fleetcontroller.samples.cpu.pb.gz"
	f, err := os.Create(path.Join(dir, name))
	if err != nil {
		log.Println("could not create CPU profile: ", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		log.Println("could not start CPU profile: ", err)
	}
	return f
}
