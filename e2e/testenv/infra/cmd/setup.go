package cmd

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
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

		var wgGit, wgHelm, wgChartMuseum sync.WaitGroup
		wgGit.Add(1)
		wgHelm.Add(1)
		wgChartMuseum.Add(1)

		go spinUpGitServer(k, &wgGit)
		go spinUpHelmRegistry(k, &wgHelm)
		go spinUpChartMuseum(k, &wgChartMuseum)

		wgHelm.Wait()

		// Login and push a Helm chart to our local Helm registry
		helmClient, err := registry.NewClient()
		if err != nil {
			fail(fmt.Errorf("create Helm registry client: %v", err))
		}

		externalIP := os.Getenv("external_ip")
		if externalIP == "" {
			externalIP, err = k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
			if err != nil {
				fail(fmt.Errorf("get external Zot service IP: %v", err))
			}
		}

		helmHost := fmt.Sprintf("%s:5000", externalIP)

		startTime := time.Now()
		for err := errors.New("not nil"); err != nil && time.Now().Sub(startTime) < 30*time.Second; {
			err = helmClient.Login(
				helmHost,
				registry.LoginOptBasicAuth("fleet-ci", "foo"),
				registry.LoginOptInsecure(true),
			)
			if err != nil {
				fmt.Println(fmt.Errorf("log into Helm registry: %v", err))

				time.Sleep(time.Second)

				fmt.Println("Trying again...")
			} else {
				fmt.Println("Success!")
			}
		}

		helmPath := os.Getenv("HELM_PATH")
		if helmPath == "" {
			helmPath = "/usr/bin/helm" // prevents eg. ~/.rd/bin/helm from being used, without support for skipping TLS
		}

		pushCmd := exec.Command(
			helmPath,
			"push",
			"sleeper-chart-0.1.0.tgz",
			fmt.Sprintf("oci://%s", helmHost),
			"--insecure-skip-tls-verify",
		)
		if _, err := pushCmd.Output(); err != nil {
			fail(fmt.Errorf("push to Helm registry: %v with output %s", err, pushCmd.Stderr))
		}

		chartArchive, err := os.ReadFile("sleeper-chart-0.1.0.tgz")
		if err != nil {
			fail(fmt.Errorf("read packaged Helm chart: %v", err))
		}

		/*
			// TODO enable this when the Helm library supports `--insecure-skip-tls-verify`
			if _, err := helmClient.Push(chartArchive, fmt.Sprintf("%s/sleeper-chart:0.1.0", helmHost)); err != nil {
				fail(fmt.Errorf("push to Helm registry: %v", err))
			}
		*/

		// Push chart to ChartMuseum
		wgChartMuseum.Wait()

		SSLCfg := &tls.Config{
			InsecureSkipVerify: true, // works around having to install or reference a CA cert
		}

		client := http.Client{
			Transport: &http.Transport{
				TLSClientConfig: SSLCfg,
			},
		}

		cmAddr := fmt.Sprintf("https://%s:8081/api/charts", externalIP)

		req, err := http.NewRequest(http.MethodPost, cmAddr, bytes.NewReader(chartArchive))
		if err != nil {
			fail(fmt.Errorf("create POST request to ChartMuseum: %v", err))
		}

		req.SetBasicAuth(os.Getenv("CI_OCI_USERNAME"), os.Getenv("CI_OCI_PASSWORD"))

		resp, err := client.Do(req)
		if err != nil {
			fail(fmt.Errorf("send POST request to ChartMuseum: %v", err))
		}

		if resp.StatusCode != http.StatusCreated {
			fail(fmt.Errorf("POST response status code from ChartMuseum: %d", resp.StatusCode))
		}

		resp.Body.Close()

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

	if err := waitForPodReady(k, "git-server"); err != nil {
		fail(fmt.Errorf("wait for git server pod to be ready: %v", err))
	}

	fmt.Println("git server up.")
}

func spinUpHelmRegistry(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	failHelm := func(err error) {
		fail(fmt.Errorf("spin up Helm registry: %v", err))
	}

	out, err := k.Create(
		"secret", "generic", "helm-secret",
		"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
		"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
		"--from-file=cacerts="+path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "root.crt"),
	)
	if err != nil {
		failHelm(fmt.Errorf("create helm-secret: %s with error %v", out, err))
	}

	out, err = k.Create(
		"secret", "tls", "helm-tls",
		"--cert", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "helm.crt"),
		"--key", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "helm.key"),
	)
	if err != nil {
		failHelm(fmt.Errorf("create helm-tls secret: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("helm/zot_secret.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("create Zot htpasswd secret: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("helm/zot_configmap.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("apply Zot config map: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("helm/zot_deployment.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("apply Zot deployment: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("helm/zot_service.yaml"))
	if err != nil {
		failHelm(fmt.Errorf("apply Zot service: %s with error %v", out, err))
	}

	if err := waitForPodReady(k, "zot"); err != nil {
		failHelm(fmt.Errorf("wait for Zot pod to be ready: %v", err))
	}

	fmt.Println("Helm registry up.")
}

func spinUpChartMuseum(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	failChartMuseum := func(err error) {
		fail(fmt.Errorf("spin up ChartMuseum: %v", err))
	}

	out, err := k.Apply("-f", testenv.AssetPath("helm/chartmuseum_deployment.yaml"))
	if err != nil {
		failChartMuseum(fmt.Errorf("apply ChartMuseum deployment: %s with error %v", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("helm/chartmuseum_service.yaml"))
	if err != nil {
		failChartMuseum(fmt.Errorf("apply ChartMuseum service: %s with error %v", out, err))
	}

	if err := waitForPodReady(k, "chartmuseum"); err != nil {
		failChartMuseum(fmt.Errorf("wait for ChartMuseum pod to be ready: %v", err))
	}

	fmt.Println("ChartMuseum up.")
}

func packageHelmChart() error {
	cmd := exec.Command("helm", "package", testenv.AssetPath("gitrepo/sleeper-chart/"))

	_, err := cmd.Output()

	if err != nil {
		err = fmt.Errorf("%s", cmd.Stderr)
	}

	return err
}

// waitForPodReady waits until a pod with the specified appName app label is ready.
func waitForPodReady(k kubectl.Command, appName string) error {
	out, err := k.Run(
		"wait",
		"--for=condition=Ready",
		"pod",
		"--timeout=30s",
		"-l",
		fmt.Sprintf("app=%s", appName),
	)

	if err != nil {
		return fmt.Errorf("waitForPodReady (appName: %s): %s, error: %v", appName, out, err)
	}

	return err
}

// fail prints err and exits.
func fail(err error) {
	fmt.Println(err.Error())

	os.Exit(1)
}
