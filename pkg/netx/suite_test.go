package netx_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNetx(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Netx Suite")
}
