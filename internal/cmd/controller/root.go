// Package controller starts the fleet controller.
package controller

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement"
	"github.com/rancher/fleet/internal/cmd/controller/gitops"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/cleanup"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/version"
)

type FleetManager struct {
	command.DebugConfig
	Kubeconfig     string `usage:"Kubeconfig file"`
	Namespace      string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	DisableMetrics bool   `usage:"disable metrics" name:"disable-metrics"`
	ShardID        string `usage:"only manage resources labeled with a specific shard ID" name:"shard-id"`
}

type ControllerReconcilerWorkers struct {
	GitRepo          int
	Bundle           int
	BundleDeployment int
}

type BindAddresses struct {
	Metrics     string
	HealthProbe string
}

var (
	setupLog = ctrl.Log.WithName("setup")
	zopts    = &zap.Options{
		Development: true,
	}
)

func (f *FleetManager) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := f.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = f.OverrideZapOpts(zopts)

	return nil
}

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log)

	kubeconfig := ctrl.GetConfigOrDie()
	workersOpts := ControllerReconcilerWorkers{}

	leaderOpts, err := command.NewLeaderElectionOptions()
	if err != nil {
		return err
	}

	bindAddresses := BindAddresses{
		Metrics:     ":8080",
		HealthProbe: ":8081",
	}
	if d := os.Getenv("FLEET_METRICS_BIND_ADDRESS"); d != "" {
		bindAddresses.Metrics = d
	}
	if d := os.Getenv("FLEET_HEALTHPROBE_BIND_ADDRESS"); d != "" {
		bindAddresses.HealthProbe = d
	}

	if d := os.Getenv("BUNDLE_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse BUNDLE_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.Bundle = w
	}
	if d := os.Getenv("BUNDLEDEPLOYMENT_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse BUNDLEDEPLOYMENT_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.BundleDeployment = w
	}

	setupCpuPprof(ctx)
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()
	if err := start(
		ctx,
		f.Namespace,
		kubeconfig,
		leaderOpts,
		workersOpts,
		bindAddresses,
		f.DisableMetrics,
		f.ShardID,
	); err != nil {
		return err
	}

	<-cmd.Context().Done()
	return nil
}

func App() *cobra.Command {
	root := command.Command(&FleetManager{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	ctrl.RegisterFlags(fs)
	root.Flags().AddGoFlagSet(fs)

	root.AddCommand(
		cleanup.App(),
		agentmanagement.App(),
		gitops.App(zopts),
	)
	return root
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
