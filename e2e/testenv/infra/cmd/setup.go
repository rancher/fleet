package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/registry"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

// setupCmd represents the setup command
var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up an end-to-end test environment",
	Long: `This sets up the git server, Helm registry and associated resources needed to run end-to-end tests.
Parallelism is used when possible to save time.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Setting up test environment...")

		repoRootCmd := exec.Command(
			"git",
			"rev-parse",
			"--show-toplevel",
		)
		repoRoot, err := repoRootCmd.Output()
		if err != nil {
			fail(fmt.Errorf("get repo root", err, repoRootCmd.Stderr))
		}

		env := testenv.New()

		// enables infra setup to be run from any location within the repo
		testenv.SetRoot(strings.TrimSpace(string(repoRoot)))

		k := env.Kubectl.Namespace(env.Namespace)

		if err := packageHelmChart(); err != nil {
			fail(fmt.Errorf("package Helm chart: %v", err))
		}

		var wgGit, wgHelm sync.WaitGroup
		wgGit.Add(1)
		wgHelm.Add(1)

		go spinUpGitServer(k, &wgGit)
		go spinUpHelmRegistry(k, &wgHelm)

		wgHelm.Wait()

		// Login and push a Helm chart to our local Helm registry
		helmClient, err := registry.NewClient()
		if err != nil {
			fail(fmt.Errorf("create Helm registry client: %v", err))
		}

		externalIP, err := k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
		if err != nil {
			fail(fmt.Errorf("get external Zot service IP: %v", err))
		}

		helmHost := fmt.Sprintf("%s:5000", externalIP)
		if err := helmClient.Login(
			helmHost,
			registry.LoginOptBasicAuth("fleet-ci", "foo"),
			registry.LoginOptInsecure(true),
		); err != nil {
			fail(fmt.Errorf("log into Helm registry: %v", err))
		}

		pushCmd := exec.Command(
			"/usr/bin/helm", // prevents eg. ~/.rd/bin/helm from being used, without support for skipping TLS
			"push",
			"sleeper-chart-0.1.0.tgz",
			fmt.Sprintf("oci://%s", helmHost),
			"--insecure-skip-tls-verify",
		)
		if _, err := pushCmd.Output(); err != nil {
			fail(fmt.Errorf("push to Helm registry: %v with output %s", err, pushCmd.Stderr))
		}

		/*
			// TODO enable this when the Helm library supports `--insecure-skip-tls-verify`
			chartArchive, err := os.ReadFile("sleeper-chart-0.1.0.tgz")
			if err != nil {
				fail(fmt.Errorf("read packaged Helm chart: %v", err))
			}

			if _, err := helmClient.Push(chartArchive, fmt.Sprintf("%s/sleeper-chart:0.1.0", helmHost)); err != nil {
				fail(fmt.Errorf("push to Helm registry: %v", err))
			}
		*/

		wgGit.Wait()
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func spinUpGitServer(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	out, err := k.Apply("-f", testenv.AssetPath("gitrepo/nginx_deployment.yaml"))
	if err != nil {
		fail(fmt.Errorf("apply git server deployment: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("gitrepo/nginx_service.yaml"))
	if err != nil {
		fail(fmt.Errorf("apply git server service: %s with error %v", out, err))
	}

	// TODO replace this with state check
	time.Sleep(5 * time.Second)

	fmt.Println("git server up.")
}

func spinUpHelmRegistry(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	failHelm := func(err error) {
		fail(fmt.Errorf("spin up Helm registry: %v", err))
	}

	out, err := k.Create(
		"secret", "generic", "helm-oci-secret",
		"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
		"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
		"--from-file=cacerts="+path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "root.crt"),
	)
	if err != nil {
		failHelm(fmt.Errorf("create helm-oci-secret: %s with error %v", out, err))
	}

	out, err = k.Create(
		"secret", "tls", "zot-tls",
		"--cert", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "zot.crt"),
		"--key", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "zot.key"),
	)
	if err != nil {
		failHelm(fmt.Errorf("create zot-tls secret: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("oci/zot_secret.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("create Zot htpasswd secret: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("oci/zot_configmap.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("apply Zot config map: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("oci/zot_deployment.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("apply Zot deployment: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("oci/zot_service.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("apply Zot service: %s with error %v", out, err))
	}

	// TODO replace this with state check
	time.Sleep(5 * time.Second)

	fmt.Println("Helm registry up.")
}

func packageHelmChart() error {
	cmd := exec.Command("helm", "package", testenv.AssetPath("gitrepo/sleeper-chart/"))

	_, err := cmd.Output()

	if err != nil {
		err = fmt.Errorf("%s", cmd.Stderr)
	}

	return err
}

// fail prints err and exits.
func fail(err error) {
	fmt.Println(err.Error())

	os.Exit(1)
}
