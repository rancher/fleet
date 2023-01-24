package helm

import (
	"context"
	"testing"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
)

var buf *gbytes.Buffer

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Suite")
}

// simulates fleet cli execution
func fleetApply(dirs []string, options *apply.Options) error {
	buf = gbytes.NewBuffer()
	options.Output = buf

	return apply.Apply(context.Background(), client.NewGetter("", "", "fleet-local"), "helm", dirs, options)
}
