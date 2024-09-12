package testenv

import (
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
	cmd := exec.Command("crust-gather", "collect",
		"--exclude-namespace=kube-system", "--exclude-kind=Lease", "--duration=5s",
		"-f", path)
	cmd.Stdout = ginkgo.GinkgoWriter
	cmd.Stderr = ginkgo.GinkgoWriter
	err := cmd.Run()
	if err != nil {
		ginkgo.GinkgoWriter.Printf("⛔ failed to gather cluster info: %v", err)
	}

	ginkgo.Fail(message, callerSkip...)
}
