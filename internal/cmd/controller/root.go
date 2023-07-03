// Package controller starts the fleet controller.
package controller

import (
	"context"
	"log"
	"net/http"
	"os"
	"path"
	"runtime/pprof"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/rancher/fleet/internal/cmd/controller/agent"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/version"

	command "github.com/rancher/wrangler-cli"
)

var (
	debugConfig command.DebugConfig
)

type FleetManager struct {
	Kubeconfig       string `usage:"Kubeconfig file"`
	Namespace        string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	DisableGitops    bool   `usage:"disable gitops components" name:"disable-gitops"`
	DisableBootstrap bool   `usage:"disable local cluster components" name:"disable-bootstrap"`
}

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	setupCpuPprof(cmd.Context())
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()
	debugConfig.MustSetupDebug()
	if err := start(cmd.Context(), f.Namespace, f.Kubeconfig, f.DisableGitops, f.DisableBootstrap); err != nil {
		return err
	}

	if debugConfig.Debug {
		agent.DebugLevel = debugConfig.DebugLevel
	}

	<-cmd.Context().Done()
	return nil
}

func App() *cobra.Command {
	cmd := command.Command(&FleetManager{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	return command.AddDebug(cmd, &debugConfig)
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
