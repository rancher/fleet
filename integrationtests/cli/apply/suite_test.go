package apply

import (
	"context"
	"testing"

	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/cmd/cli/apply"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var buf *gbytes.Buffer

func TestFleetApply(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Apply Suite")
}

// simulates fleet cli execution
func fleetApply(name string, dirs []string, options apply.Options) error {
	buf = gbytes.NewBuffer()
	options.Output = buf
	return apply.CreateBundles(context.Background(), client.NewGetter("", "", "fleet-local"), name, dirs, options)
}

// simulates fleet cli execution in driven mode
func fleetApplyDriven(name string, dirs []string, options apply.Options) error {
	buf = gbytes.NewBuffer()
	options.DrivenScan = true
	options.Output = buf
	options.DrivenScanSeparator = ":"
	return apply.CreateBundlesDriven(context.Background(), client.NewGetter("", "", "fleet-local"), name, dirs, options)
}

// simulates fleet cli online execution, with mocked client
func fleetApplyOnline(getter apply.Getter, name string, dirs []string, options apply.Options) error {
	return apply.CreateBundles(context.Background(), getter, name, dirs, options)
}
