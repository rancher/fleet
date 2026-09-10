package bundleevents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/config"
)

// rendered is the deploymentEvents section as the fleet chart renders it into
// the controller's configmap.
const rendered = `{
  "deploymentEvents": {
    "disabled": false,
    "debounce": "10s",
    "minInterval": "2m",
    "reportRecovery": false,
    "perDeployment": true,
    "maxCauses": 5
  }
}`

func TestOptionsFromRenderedConfig(t *testing.T) {
	cfg := &config.Config{}
	if err := json.Unmarshal([]byte(rendered), cfg); err != nil {
		t.Fatalf("reading config: %v", err)
	}

	opts := OptionsFromConfig(cfg)

	if !opts.Enabled {
		t.Error("expected events to be enabled")
	}
	if opts.Debounce != 10*time.Second {
		t.Errorf("unexpected debounce: %s", opts.Debounce)
	}
	if opts.MinInterval != 2*time.Minute {
		t.Errorf("unexpected minimum interval: %s", opts.MinInterval)
	}
	if opts.ReportRecovery {
		t.Error("expected recovery reporting to be off")
	}
	if !opts.PerDeployment {
		t.Error("expected per-deployment reporting to be on")
	}
	if opts.MaxCauses != 5 {
		t.Errorf("unexpected number of causes: %d", opts.MaxCauses)
	}
}

func TestOptionsFallBackToDefaults(t *testing.T) {
	opts := OptionsFromConfig(&config.Config{})
	defaults := DefaultOptions()

	if opts != defaults {
		t.Errorf("expected the defaults for an empty config, got %+v", opts)
	}
}

func TestOptionsCanBeDisabled(t *testing.T) {
	cfg := &config.Config{DeploymentEvents: config.DeploymentEvents{Disabled: true}}

	if OptionsFromConfig(cfg).Enabled {
		t.Error("expected events to be disabled")
	}
}
