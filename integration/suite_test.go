package integration_test

import (
	"path"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const root = ".."

func examplePath(p ...string) string {
	parts := append([]string{root, "fleet-examples"}, p...)
	return path.Join(parts...)
}

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bundle Suite")
}
