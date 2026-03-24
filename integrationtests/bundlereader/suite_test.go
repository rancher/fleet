package bundlereader_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBundleReader(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BundleReader Suite")
}
