package main

import (
	"context"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path"
	"runtime/pprof"
	"time"

	"github.com/rancher/fleet/pkg/durations"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/rancher/fleet/pkg/agent"
	"github.com/rancher/fleet/pkg/fleetcontroller"
	"github.com/rancher/fleet/pkg/version"
	command "github.com/rancher/wrangler-cli"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/networking.k8s.io"
	"github.com/spf13/cobra"
)

var (
	debugConfig command.DebugConfig
)

type FleetManager struct {
	Kubeconfig    string `usage:"Kubeconfig file"`
	Namespace     string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	DisableGitops bool   `usage:"disable gitops components" name:"disable-gitops"`
}

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	setupCpuPprof(cmd.Context())
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	debugConfig.MustSetupDebug()
	if err := fleetcontroller.Start(cmd.Context(), f.Namespace, f.Kubeconfig, f.DisableGitops); err != nil {
		return err
	}

	if debugConfig.Debug {
		agent.DebugLevel = debugConfig.DebugLevel
	}

	<-cmd.Context().Done()
	return nil
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

func main() {
	cmd := command.Command(&FleetManager{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	cmd = command.AddDebug(cmd, &debugConfig)
	command.Main(cmd)
}
