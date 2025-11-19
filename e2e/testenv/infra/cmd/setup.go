package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/chartmuseum/helm-push/pkg/chartmuseum"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"helm.sh/helm/v4/pkg/registry"
)

var timeoutDuration = 10 * time.Minute // default timeout duration

const InfraNamespace = "default"

func eventually(f func() (string, error)) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			fail(fmt.Errorf("timed out: %w", ctx.Err()))
		default:
			out, err := f()
			if err != nil {
				msg := fmt.Sprintf("error: %v", err)
				if out != "" {
					msg = fmt.Sprintf("%s: stdout: %s", msg, out)
				}
				fmt.Println(msg)
				time.Sleep(time.Second)
				continue
			}
			return out
		}
	}
}

// setupCmd represents the setup command
var setupCmd = &cobra.Command{
	Use:   "setup [--git-server=(true|false)|--helm-registry=(true|false)|--oci-registry=(true|false)]",
	Short: "Set up an end-to-end test environment",
	Long: `This sets up the git server, Helm registry, OCI registry and associated resources needed to run
    end-to-end tests. If no argument is specified, then the whole infra is set up at once. Parallelism is used when
    possible to save time.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Setting up test environment...")

		var err error
		if minutes := os.Getenv("CI_OCI_TIMEOUT_DURATION"); minutes != "" {
			timeoutDuration, err = time.ParseDuration(minutes)
			if err != nil {
				fail(fmt.Errorf("parse timeout duration: %w", err))
			}
		}
		fmt.Printf("Timeout duration: %v\n", timeoutDuration)

		repoRootCmd := exec.Command(
			"git",
			"rev-parse",
			"--show-toplevel",
		)
		cwd, err := os.Getwd()
		if err != nil {
			fail(err)
		}
		repoRoot, err := repoRootCmd.CombinedOutput()
		if err != nil {
			fail(fmt.Errorf("get repo root in %s: output: %q, error: %w", cwd, repoRoot, err))
		}

		env := testenv.New()

		// enables infra setup to be run from any location within the repo
		testenv.SetRoot(strings.TrimSpace(string(repoRoot)))

		k := env.Kubectl.Namespace(InfraNamespace)

		if err := packageHelmChart(); err != nil {
			fail(fmt.Errorf("package Helm chart: %w", err))
		}

		// Only act on specified components, unless none is specified in which case all are affected.
		if !withGitServer && !withHelmRegistry && !withOCIRegistry {
			withGitServer, withHelmRegistry, withOCIRegistry = true, true, true
		}

		var wgGit, wgHelm, wgOCI sync.WaitGroup

		if withGitServer {
			wgGit.Add(1)
			go spinUpGitServer(k, &wgGit)
		}

		externalIP := os.Getenv("external_ip")

		if withHelmRegistry || withOCIRegistry {
			out, err := k.Create(
				"secret", "tls", "helm-tls",
				"--cert", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "helm.crt"),
				"--key", path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "helm.key"),
			)
			if err != nil && !strings.Contains(out, "already exists") {
				fail(fmt.Errorf("create helm-tls secret: %s with error %w", out, err))
			}

			out, err = k.Namespace(env.Namespace).Create(
				"secret", "generic", "helm-secret",
				"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
				"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
				"--from-file=cacerts="+path.Join(os.Getenv("CI_OCI_CERTS_DIR"), "root.crt"),
			)
			if err != nil && !strings.Contains(out, "already exists") {
				fail(fmt.Errorf("create helm-secret: %s with error %w", out, err))
			}
		}

		if withOCIRegistry {
			wgOCI.Add(1)
			go spinUpOCIRegistry(k, &wgOCI)
			wgOCI.Wait()

			// Login and push a Helm chart to our local OCI registry
			tlsConf := &tls.Config{
				InsecureSkipVerify: true,
			}
			OCIClient, err := registry.NewClient(
				registry.ClientOptHTTPClient(&http.Client{
					Transport: &http.Transport{
						TLSClientConfig: tlsConf,
						Proxy:           http.ProxyFromEnvironment,
					},
				}),
			)
			if err != nil {
				fail(fmt.Errorf("create OCI registry client: %w", err))
			}

			if externalIP == "" {
				externalIP = waitForLoadbalancer(k, "zot-service")
			}

			OCIHost := fmt.Sprintf("%s:8082", externalIP)

			fmt.Printf("logging into OCI registry at %s...\n", OCIHost)
			_ = eventually(func() (string, error) {
				err := OCIClient.Login(
					OCIHost,
					registry.LoginOptBasicAuth(os.Getenv("CI_OCI_USERNAME"), os.Getenv("CI_OCI_PASSWORD")),
					registry.LoginOptInsecure(true),
				)
				if err != nil {
					return "", fmt.Errorf("logging into OCI registry: %w", err)
				}

				return "", nil
			})

			chartArchive, err := os.ReadFile("sleeper-chart-0.1.0.tgz")
			if err != nil {
				fail(fmt.Errorf("reading helm chart: %w", err))
			}
			if _, err := OCIClient.Push(chartArchive, fmt.Sprintf("%s/sleeper-chart:0.1.0", OCIHost)); err != nil {
				fail(fmt.Errorf("push to OCI registry: %w", err))
			}
		}

		if withHelmRegistry {
			wgHelm.Add(1)
			go spinUpHelmRegistry(k, &wgHelm)

			// Push chart to ChartMuseum
			wgHelm.Wait()

			if externalIP == "" {
				externalIP = waitForLoadbalancer(k, "chartmuseum-service")
			}

			_ = eventually(func() (string, error) {
				c, err := chartmuseum.NewClient(
					chartmuseum.URL(fmt.Sprintf("https://%s:8081", externalIP)),
					chartmuseum.Username(os.Getenv("CI_OCI_USERNAME")),
					chartmuseum.Password(os.Getenv("CI_OCI_PASSWORD")),
					chartmuseum.InsecureSkipVerify(true),
				)
				if err != nil {
					return "", fmt.Errorf("creating chartmuseum client: %w", err)
				}
				resp, err := c.UploadChartPackage("sleeper-chart-0.1.0.tgz", true)
				if err != nil {
					return "", fmt.Errorf("POST request to ChartMuseum failed: %w", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusCreated {
					return "", fmt.Errorf("POST response status code from ChartMuseum: %d", resp.StatusCode)
				}
				fmt.Println("successfully posted Helm chart to ChartMuseum")

				return "", nil
			})
		}

		if withGitServer {
			wgGit.Wait()
		}
	},
}

func spinUpGitServer(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	out, err := k.Apply("-f", testenv.AssetPath("gitrepo/nginx_deployment.yaml"))
	if err != nil {
		fail(fmt.Errorf("apply git server deployment: %s with error %w", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("gitrepo/nginx_service.yaml"))
	if err != nil {
		fail(fmt.Errorf("apply git server service: %s with error %w", out, err))
	}

	waitForPodReady(k, "git-server")
	waitForLoadbalancer(k, "git-service")

	fmt.Println("git server up.")
}

func spinUpOCIRegistry(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	failOCI := func(err error) {
		fail(fmt.Errorf("spin up OCI registry: %w", err))
	}

	var err error
	htpasswd := "fleet-ci:$2y$05$0WcEGGqsUKcyPhBFU7l07uJ3ND121p/FQCY90Q.dcsZjTkr.b45Lm"
	if os.Getenv("CI_OCI_USERNAME") != "" && os.Getenv("CI_OCI_PASSWORD") != "" {
		p, err := bcrypt.GenerateFromPassword([]byte(os.Getenv("CI_OCI_PASSWORD")), bcrypt.MinCost)
		if err != nil {
			fail(fmt.Errorf("generate bcrypt password from env var: %w", err))
		}
		htpasswd = fmt.Sprintf("%s:%s\n", os.Getenv("CI_OCI_USERNAME"), string(p))
	}
	if os.Getenv("CI_OCI_READER_USERNAME") != "" && os.Getenv("CI_OCI_READER_PASSWORD") != "" {
		p, err := bcrypt.GenerateFromPassword([]byte(os.Getenv("CI_OCI_READER_PASSWORD")), bcrypt.MinCost)
		if err != nil {
			fail(fmt.Errorf("generate bcrypt password from env var: %w", err))
		}
		htpasswd += fmt.Sprintf("\n%s:%s\n", os.Getenv("CI_OCI_READER_USERNAME"), string(p))
	}
	if os.Getenv("CI_OCI_NO_DELETER_USERNAME") != "" && os.Getenv("CI_OCI_NO_DELETER_PASSWORD") != "" {
		p, err := bcrypt.GenerateFromPassword([]byte(os.Getenv("CI_OCI_NO_DELETER_PASSWORD")), bcrypt.MinCost)
		if err != nil {
			fail(fmt.Errorf("generate bcrypt password from env var: %w", err))
		}
		htpasswd += fmt.Sprintf("\n%s:%s", os.Getenv("CI_OCI_NO_DELETER_USERNAME"), string(p))
	}

	err = testenv.ApplyTemplate(k, "helm/zot_secret.yaml", struct{ HTTPPasswd string }{htpasswd})
	if err != nil {
		failOCI(fmt.Errorf("create Zot htpasswd secret: %w", err))
	}

	zotConfig, err := getZotConfig()
	if err != nil {
		failOCI(fmt.Errorf("getting Zot config: %w", err))
	}

	err = testenv.ApplyTemplate(k, "helm/zot_configmap.yaml", struct{ ZotConfig template.HTML }{template.HTML(zotConfig)})
	if err != nil {
		failOCI(fmt.Errorf("apply Zot config map with error %w", err))
	}

	out, err := k.Apply("-f", testenv.AssetPath("helm/zot_deployment.yaml"))
	if err != nil {
		failOCI(fmt.Errorf("apply Zot deployment: %s with error %w", out, err))
	}

	out, err = k.Apply("-f", testenv.AssetPath("helm/zot_service.yaml"))
	if err != nil {
		failOCI(fmt.Errorf("apply Zot service: %s with error %w", out, err))
	}

	waitForPodReady(k, "zot")

	fmt.Println("OCI registry up.")
}

func spinUpHelmRegistry(k kubectl.Command, wg *sync.WaitGroup) {
	defer wg.Done()

	failChartMuseum := func(err error) {
		fail(fmt.Errorf("spin up ChartMuseum: %w", err))
	}

	err := testenv.ApplyTemplate(k, "helm/chartmuseum_deployment.yaml",
		struct {
			User     string
			Password string
		}{
			os.Getenv("CI_OCI_USERNAME"),
			os.Getenv("CI_OCI_PASSWORD"),
		},
	)
	if err != nil {
		failChartMuseum(fmt.Errorf("apply ChartMuseum deployment: %w", err))
	}

	out, err := k.Apply("-f", testenv.AssetPath("helm/chartmuseum_service.yaml"))
	if err != nil {
		failChartMuseum(fmt.Errorf("apply ChartMuseum service: %s with error %w", out, err))
	}

	waitForPodReady(k, "chartmuseum")

	fmt.Println("ChartMuseum up.")
}

func packageHelmChart() error {
	cmd := exec.CommandContext(context.Background(), "helm", "package", testenv.AssetPath("gitrepo/sleeper-chart/"))

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

func waitForLoadbalancer(k kubectl.Command, name string) string {
	ip := eventually(func() (string, error) {
		return k.Get("service", name, "-o", "jsonpath={.status.loadBalancer.ingress[0].ip}")
	})
	return ip
}

// fail prints err and exits.
func fail(err error) {
	fmt.Println(err.Error())

	os.Exit(1)
}

func getZotConfig() (string, error) {
	configTemplate := `{
    "http": {
        "auth": {
            "htpasswd": {
                "path": "/secret/htpasswd"
            }
        },
        "tls": {
            "cert": "/etc/zot/certs/tls.crt",
            "key": "/etc/zot/certs/tls.key"
        },
        "accessControl": {
            "repositories": {
                "**": {
                    "policies": [
                        {
                            "users": [
                                "%s"
                            ],
                            "actions": [
                                "read",
                                "create",
                                "update",
                                "delete"
                            ]
                        },
                        {
                            "users": [
                                "%s"
                            ],
                            "actions": [
                                "read"
                            ]
                        },
                        {
                            "users": [
                                "%s"
                            ],
                            "actions": [
                                "read",
                                "create"
                            ]
                        }
                    ],
                    "defaultPolicy": []
                }
            }
        },
        "address": "0.0.0.0",
        "port": "8082"
    },
    "log": {
        "level": "debug"
    },
    "storage": {
        "rootDirectory": "/tmp/zot"
    },
    "extensions": {
        "ui": {
            "enable": true
        },
        "search": {
            "enable": true
        }
    }
    }`
	reader, err := getEnvVarUser("CI_OCI_READER_USERNAME")
	if err != nil {
		return "", err
	}
	writer, err := getEnvVarUser("CI_OCI_USERNAME")
	if err != nil {
		return "", err
	}
	noDeleter, err := getEnvVarUser("CI_OCI_NO_DELETER_USERNAME")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(configTemplate, writer, reader, noDeleter), nil
}

func getEnvVarUser(username string) (string, error) {
	u, ok := os.LookupEnv(username)
	if !ok {
		return "", fmt.Errorf("%s is not set", username)
	}

	return u, nil
}
