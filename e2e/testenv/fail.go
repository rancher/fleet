package testenv

import (
	"context"
	"os"
	"os/exec"
	"path"

	ginkgo "github.com/onsi/ginkgo/v2"
)

func FailAndGather(message string, callerSkip ...int) {
	if _, err := exec.LookPath("crust-gather"); err != nil {
		ginkgo.GinkgoWriter.Print("⛔ crust-gather is not available, not dumping cluster info")
		ginkgo.Fail(message, callerSkip...)
	}

	pwd := os.Getenv("GITHUB_WORKSPACE")
	if pwd != "" {
		pwd = path.Join(pwd, "tmp", "upstream")
	} else {
		pwd = path.Join(os.TempDir(), "fleet-gather")
	}
	path := path.Join(pwd, ginkgo.CurrentSpecReport().FullText())

	ginkgo.GinkgoWriter.Printf("💬 Gathering cluster info for '%s' to '%s'...\n", ginkgo.CurrentSpecReport().FullText(), pwd)
	cmd := exec.CommandContext(context.Background(), "crust-gather", "collect",
		"--exclude-namespace=kube-system", "--exclude-kind=Lease", "--duration=10s",
		"-verror", "-f", path)
	cmd.Stdout = ginkgo.GinkgoWriter
	cmd.Stderr = ginkgo.GinkgoWriter
	// Outputting errors, but don't care about error code as crust-gather
	// often runs into a "deadline" error. Data collection is successful
	// nevertheless.
	_ = cmd.Run()

	ginkgo.Fail(message, callerSkip...)
}
