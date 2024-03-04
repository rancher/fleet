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
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"

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
	DisableGitops  bool   `usage:"disable gitops components" name:"disable-gitops"`
	DisableMetrics bool   `usage:"disable metrics" name:"disable-metrics"`
}

type LeaderElectionOptions struct {
	// LeaseDuration is the duration that non-leader candidates will
	// wait to force acquire leadership. This is measured against time of
	// last observed ack. Default is 15 seconds.
	LeaseDuration *time.Duration

	// RenewDeadline is the duration that the acting controlplane will retry
	// refreshing leadership before giving up. Default is 10 seconds.
	RenewDeadline *time.Duration

	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions. Default is 2 seconds.
	RetryPeriod *time.Duration
}

type BindAddresses struct {
	Metrics     string
	HealthProbe string
}

var (
	setupLog = ctrl.Log.WithName("setup")
	zopts    = zap.Options{
		Development: true,
	}
)

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

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log)
	// for compatibility, override zap opts with legacy debug opts. remove once manifests are updated.
	zopts.Development = f.Debug
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	kubeconfig := ctrl.GetConfigOrDie()

	leaderOpts := LeaderElectionOptions{}
	if d := os.Getenv("CATTLE_ELECTION_LEASE_DURATION"); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CATTLE_ELECTION_LEASE_DURATION", "duration", d)
			return err

		}
		leaderOpts.LeaseDuration = &v
	}
	if d := os.Getenv("CATTLE_ELECTION_RENEW_DEADLINE"); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CATTLE_ELECTION_RENEW_DEADLINE", "duration", d)
			return err
		}
		leaderOpts.RenewDeadline = &v
	}
	if d := os.Getenv("CATTLE_ELECTION_RETRY_PERIOD"); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CATTLE_ELECTION_RETRY_PERIOD", "duration", d)
			return err
		}
		leaderOpts.RetryPeriod = &v
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

	setupCpuPprof(ctx)
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()
	if err := start(
		ctx, f.Namespace,
		kubeconfig,
		leaderOpts,
		bindAddresses,
		f.DisableGitops,
		f.DisableMetrics,
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

	root.AddCommand(cleanup.App())
	root.AddCommand(agentmanagement.App())
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
