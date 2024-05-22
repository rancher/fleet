package git

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	timeout = 30 * time.Second
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Git Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
})
