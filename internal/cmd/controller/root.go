// Package controller starts the fleet controller.
package controller

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement"
	"github.com/rancher/fleet/internal/cmd/controller/gitops"
	"github.com/rancher/fleet/internal/cmd/controller/helmops"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/cleanup"
	"github.com/rancher/fleet/pkg/version"
)

type FleetController struct {
	command.DebugConfig
	Kubeconfig           string `usage:"Kubeconfig file"`
	Namespace            string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	DisableMetrics       bool   `usage:"disable metrics" name:"disable-metrics"`
	ShardID              string `usage:"only manage resources labeled with a specific shard ID" name:"shard-id"`
	EnableLeaderElection bool   `name:"leader-elect" default:"true" usage:"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager."`
}

type ControllerReconcilerWorkers struct {
	Bundle           int
	BundleDeployment int
	Cluster          int
	ClusterGroup     int
	ImageScan        int
	Schedule         int
	Content          int
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

func (f *FleetController) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := f.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = f.OverrideZapOpts(zopts)

	return nil
}

func (f *FleetController) Run(cmd *cobra.Command, args []string) error {
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

	if d := os.Getenv("CLUSTER_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CLUSTER_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.Cluster = w
	}

	if d := os.Getenv("CLUSTERGROUP_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CLUSTERGROUP_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.ClusterGroup = w
	}

	if d := os.Getenv("IMAGESCAN_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse IMAGESCAN_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.ImageScan = w
	}

	if d := os.Getenv("SCHEDULE_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse SCHEDULE_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.Schedule = w
	}

	if d := os.Getenv("CONTENT_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CONTENT_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.Content = w
	}

	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) //nolint:gosec // Debugging only
	}()
	if err := start(
		ctx,
		f.Namespace,
		kubeconfig,
		f.EnableLeaderElection,
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
	root := command.Command(&FleetController{}, cobra.Command{
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
		helmops.App(zopts),
	)
	return root
}
