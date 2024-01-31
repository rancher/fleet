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
