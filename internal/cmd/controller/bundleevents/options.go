package bundleevents

import (
	"time"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/pkg/durations"
)

const (
	// defaultMaxCauses is how many distinct failure causes a single event describes.
	defaultMaxCauses = 3

	// defaultQPS and defaultBurst pace event creation across all bundles. A
	// cluster going away fails every bundle targeting it, and those events
	// cannot be merged, as they are about different objects.
	defaultQPS   = 5
	defaultBurst = 25

	// defaultMaxTracked bounds the number of objects the emitter remembers.
	defaultMaxTracked = 20000
)

// Options configure which deployment state changes are reported as events, and
// how often.
type Options struct {
	// Enabled turns event reporting on or off.
	Enabled bool

	// Debounce is how long to wait for a burst of failures to settle before
	// reporting it, so that the reported counts and causes describe the
	// whole burst instead of only its first failure.
	Debounce time.Duration

	// MinInterval is the minimum time between two events for the same
	// object. It bounds how often a flapping deployment reports.
	MinInterval time.Duration

	// ReportRecovery reports bundles whose deployments all became ready
	// again after a failure.
	ReportRecovery bool

	// PerDeployment additionally reports each failing bundle deployment.
	// This is more detailed, but produces one event per failing deployment,
	// in the namespace of its cluster.
	PerDeployment bool

	// MaxCauses is how many distinct failure causes a single event describes.
	MaxCauses int

	// MaxTracked is the number of objects the emitter remembers in order to
	// deduplicate their events.
	MaxTracked int
}

// DefaultOptions returns the options used when nothing is configured.
func DefaultOptions() Options {
	return Options{
		Enabled:        true,
		Debounce:       durations.BundleEventsDebounce,
		MinInterval:    durations.BundleEventsMinInterval,
		ReportRecovery: true,
		PerDeployment:  false,
		MaxCauses:      defaultMaxCauses,
		MaxTracked:     defaultMaxTracked,
	}
}

// OptionsFromConfig turns the fleet controller's config into options. Unset
// durations keep their default, as elsewhere in the config.
func OptionsFromConfig(cfg *config.Config) Options {
	opts := DefaultOptions()
	if cfg == nil {
		return opts
	}

	events := cfg.DeploymentEvents

	if events.Disabled {
		opts.Enabled = false
	}
	if events.Debounce.Duration > 0 {
		opts.Debounce = events.Debounce.Duration
	}
	if events.MinInterval.Duration > 0 {
		opts.MinInterval = events.MinInterval.Duration
	}
	if events.ReportRecovery != nil {
		opts.ReportRecovery = *events.ReportRecovery
	}
	if events.PerDeployment {
		opts.PerDeployment = true
	}
	if events.MaxCauses > 0 {
		opts.MaxCauses = events.MaxCauses
	}

	return opts
}

// OptionsFromGlobalConfig reads the options from the fleet controller's config.
// It is read on every use, so that changes to the configmap take effect without
// a restart.
func OptionsFromGlobalConfig() Options {
	return OptionsFromConfig(config.Get())
}
