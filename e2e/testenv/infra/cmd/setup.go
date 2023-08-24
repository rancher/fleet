package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/registry"
)

var timeoutDuration = 10 * time.Minute // default timeout duration

func eventually(f func() (string, error)) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			fail(fmt.Errorf("timed out: %v", ctx.Err()))
		default:
			out, err := f()
			if err != nil {
				fmt.Printf("error: %v\n", err)
				time.Sleep(time.Second)
				continue
			}
			return out
		}
	}
}

// setupCmd represents the setup command
var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up an end-to-end test environment",
	Long: `This sets up the git server, Helm registry and associated resources needed to run end-to-end tests.
Parallelism is used when possible to save time.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Setting up test environment...")

		var err error
		if minutes := os.Getenv("CI_OCI_TIMEOUT_DURATION"); minutes != "" {
			timeoutDuration, err = time.ParseDuration(minutes)
			if err != nil {
				fail(fmt.Errorf("parse timeout duration: %v", err))
			}
		}
		fmt.Printf("Timeout duration: %v\n", timeoutDuration)

		repoRootCmd := exec.Command(
			"git",
			"rev-parse",
			"--show-toplevel",
		)
		repoRoot, err := repoRootCmd.Output()
		if err != nil {
			fail(fmt.Errorf("get repo root: %v", err))
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
			externalIP = eventually(func() (string, error) {
				return k.Get("service", "zot-service", "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
			})
		}

		helmHost := fmt.Sprintf("%s:5000", externalIP)

		fmt.Printf("logging into Helm registry at %s...\n", helmHost)
		_ = eventually(func() (string, error) {
			err := helmClient.Login(
				helmHost,
				registry.LoginOptBasicAuth("fleet-ci", "foo"),
				registry.LoginOptInsecure(true),
			)
			if err != nil {
				return "", fmt.Errorf("logging into Helm registry: %v", err)
			}

			return "", nil
		})

		fmt.Println("determining Helm binary path...")
		helmPath := os.Getenv("HELM_PATH")
		if helmPath == "" {
			helmPath = "/usr/bin/helm" // prevents eg. ~/.rd/bin/helm from being used, without support for skipping TLS
		}

		fmt.Println("pushing Helm chart to registry...")
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

		_ = eventually(func() (string, error) {
			SSLCfg := &tls.Config{
				InsecureSkipVerify: true, // works around having to install or reference a CA cert
			}

			client := http.Client{
				Timeout: 10 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig:       SSLCfg,
					IdleConnTimeout:       10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
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
				return "", fmt.Errorf("POST request to ChartMuseum failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				return "", fmt.Errorf("POST response status code from ChartMuseum: %d", resp.StatusCode)
			}
			fmt.Println("successfully posted Helm chart to ChartMuseum")

			return "", nil
		})

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

	waitForPodReady(k, "git-server")

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

	waitForPodReady(k, "zot")

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

	waitForPodReady(k, "chartmuseum")

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
func waitForPodReady(k kubectl.Command, appName string) {
	_ = eventually(func() (string, error) {
		out, err := k.Run(
			"wait",
			"--for=condition=Ready",
			"pod",
			"--timeout=30s",
			"-l",
			fmt.Sprintf("app=%s", appName),
		)

		if err != nil {
			fmt.Printf("waitForPodReady (appName: %s): %s, error: %v", appName, out, err)
			return "", err
		}

		return "", nil
	})
}

// fail prints err and exits.
func fail(err error) {
	fmt.Println(err.Error())

	os.Exit(1)
}
