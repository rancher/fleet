package target_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/onsi/gomega/gbytes"

	clihelper "github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/schemes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet CLI target", func() {
	var args []string

	act := func(args []string) (*gbytes.Buffer, *gbytes.Buffer, error) {
		cmd := cli.NewTarget()
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		errBuf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(errBuf)
		err := cmd.Execute()
		return buf, errBuf, err
	}

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			})).ToNot(HaveOccurred())
		})
	})

	When("input file parameter is missing", func() {
		It("prints the help", func() {
			buf, _, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("Usage:"))
		})
	})

	When("Input file is missing", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", "/tmp/does-not-exist-bundle.yaml"}
		})

		It("prints an error", func() {
			_, errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("no such file or directory"))
		})
	})

	When("Input file is invalid", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "helmrepository/config-chart-0.1.0.tgz"}
		})

		It("prints an error", func() {
			_, errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("error converting YAML to JSON"))
		})
	})

	When("Input file does not contain a bundle", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "helmrepository/index.yaml"}
		})

		It("prints an error", func() {
			_, errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("bundle is empty"))
		})
	})

	When("No cluster in namespace", func() {
		BeforeEach(func() {
			args = []string{"--bundle-file", clihelper.AssetsPath + "bundle/bundle.yaml"}
		})

		It("prints a manifest only", func() {
			args = append(args, "--dump-input-list")
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("null"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf.Contents()).NotTo(ContainSubstring("kind: BundleDeployment"))
		})
	})

	When("two GitRepos reuse dependency templates on the same target cluster", func() {
		It("generates distinct bundle names and resolves each instance's own prerequisites", func() {
			By("preparing shared source and an operator-managed target cluster")
			source := GinkgoT().TempDir()
			Expect(os.CopyFS(filepath.Join(source, "app"), os.DirFS(clihelper.AssetsPath+"dependency-templates"))).To(Succeed())
			GinkgoT().Chdir(source)
			Expect(schemes.Register(fleetv1.AddToScheme)).To(Succeed())

			registrationNamespace, err := utils.NewNamespaceName()
			Expect(err).NotTo(HaveOccurred())
			deploymentNamespace, err := utils.NewNamespaceName()
			Expect(err).NotTo(HaveOccurred())
			for _, name := range []string{registrationNamespace, deploymentNamespace} {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
					Name: name, Labels: map[string]string{"kubernetes.io/metadata.name": name},
				}}
				Expect(k8sClient.Create(ctx, ns)).To(Succeed())
				DeferCleanup(func() { Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })
			}
			cluster := &fleetv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "downstream", Namespace: registrationNamespace}}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cluster.Status.Namespace = deploymentNamespace
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, &fleetv1.BundleNamespaceMapping{
				ObjectMeta:     metav1.ObjectMeta{Name: "targets", Namespace: namespace},
				BundleSelector: &metav1.LabelSelector{},
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": registrationNamespace,
				}},
			})).To(Succeed())

			targetsFile := filepath.Join(source, "targets.json")
			targets, err := json.Marshal(fleetv1.BundleSpec{
				Targets:            []fleetv1.BundleTarget{{ClusterName: "downstream"}},
				TargetRestrictions: []fleetv1.BundleTargetRestriction{{ClusterName: "downstream"}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(targetsFile, targets, 0600)).To(Succeed())
			applications := []*fleetv1.BundleDeployment{}
			for _, name := range []string{"application-dev", "application-qa"} {
				By("generating and targeting " + name + " without explicit bundle names")
				repo := &fleetv1.GitRepo{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
					Spec: fleetv1.GitRepoSpec{
						Repo: "https://example.com/application.git", Paths: []string{"app"},
						TargetNamespace: name, Targets: []fleetv1.GitTarget{{ClusterName: "downstream"}},
					},
				}
				Expect(k8sClient.Create(ctx, repo)).To(Succeed())
				output := gbytes.NewBuffer()
				// These are the same identity and target inputs GitJob passes to fleet apply.
				Expect(apply.CreateBundles(ctx, k8sClient, nil, repo.Name, repo.Spec.Paths, apply.Options{
					Namespace: repo.Namespace, Labels: map[string]string{fleetv1.RepoLabel: repo.Name},
					TargetNamespace: repo.Spec.TargetNamespace, TargetsFile: targetsFile, Output: output,
				})).To(Succeed())
				bundles, err := clihelper.GetBundleListFromOutput(output)
				Expect(err).NotTo(HaveOccurred())
				Expect(bundles).To(HaveLen(3))
				for _, bundle := range bundles {
					component := bundle.Labels["example.com/component"]
					Expect(bundle.Name).To(Equal(name + "-app-" + component))
					Expect(k8sClient.Create(ctx, bundle)).To(Succeed())
					input, err := json.Marshal(bundle)
					Expect(err).NotTo(HaveOccurred())
					bundleFile := filepath.Join(source, "bundle.json")
					Expect(os.WriteFile(bundleFile, input, 0600)).To(Succeed())
					var originalSpec []byte
					for attempt := range 2 {
						buf, _, err := act([]string{"--bundle-file", bundleFile})
						Expect(err).NotTo(HaveOccurred())
						decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(buf.Contents()), 4096)
						deployments := []*fleetv1.BundleDeployment{}
						for {
							bd := &fleetv1.BundleDeployment{}
							err := decoder.Decode(bd)
							if errors.Is(err, io.EOF) {
								break
							}
							Expect(err).NotTo(HaveOccurred())
							if bd.Kind == "BundleDeployment" {
								deployments = append(deployments, bd)
							}
						}
						Expect(deployments).To(HaveLen(1))
						bd := deployments[0]
						Expect(bd.Name).To(Equal(bundle.Name))
						Expect(bd.Namespace).To(Equal(deploymentNamespace))
						Expect(bd.Spec.Options.TargetNamespace).To(Equal(name))
						spec, err := json.Marshal(bd.Spec)
						Expect(err).NotTo(HaveOccurred())
						if attempt == 0 {
							originalSpec = spec
							Expect(k8sClient.Create(ctx, bd)).To(Succeed())
							if component == "application" {
								applications = append(applications, bd)
							}
						} else {
							Expect(spec).To(MatchJSON(originalSpec))
						}
					}
				}
			}
			By("checking instance-local matches and preserved source expressions")
			all := &fleetv1.BundleDeploymentList{}
			Expect(k8sClient.List(ctx, all, client.InNamespace(deploymentNamespace))).To(Succeed())
			Expect(all.Items).To(HaveLen(6))
			Expect(applications).To(HaveLen(2))
			for _, application := range applications {
				name := application.Labels[fleetv1.RepoLabel]
				Expect(application.Spec.DependsOn).To(HaveLen(2))
				for _, dependency := range application.Spec.DependsOn {
					Expect(dependency.Selector.MatchLabels).To(HaveKeyWithValue(fleetv1.RepoLabel, name))
					Expect(dependency.Selector.MatchLabels).To(HaveKeyWithValue(fleetv1.BundleNamespaceLabel, namespace))
					selector, err := metav1.LabelSelectorAsSelector(dependency.Selector)
					Expect(err).NotTo(HaveOccurred())
					matches := &fleetv1.BundleDeploymentList{}
					Expect(k8sClient.List(ctx, matches, client.InNamespace(deploymentNamespace), client.MatchingLabelsSelector{Selector: selector})).To(Succeed())
					Expect(matches.Items).To(HaveLen(1))
					Expect(matches.Items[0].Name).To(Equal(name + "-app-" + dependency.Selector.MatchLabels["example.com/component"]))
				}
				bundle := &fleetv1.Bundle{}
				Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: application.Name}, bundle)).To(Succeed())
				Expect(bundle.Spec.DependsOn[0].Selector.MatchLabels[fleetv1.RepoLabel]).To(Equal("${ .GitRepo.Name }"))
			}
		})
	})

	When("Matching a cluster in namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--bundle-file", clihelper.AssetsPath + "bundle/bundle.yaml",
				"--namespace", namespace,
				"--dump-input-list",
			}

			err := k8sClient.Create(ctx, &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local",
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("prints a manifest and bundledeployment", func() {
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(errBuf.Contents())).To(ContainSubstring("- Bundle:"), "cannot list inputs")
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
		})
	})

	When("Matching multiple clusters in namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--bundle-file", clihelper.AssetsPath + "bundle/bundle-all.yaml",
				"--namespace", namespace,
				"--dump-input-list",
			}

			err := k8sClient.Create(ctx, &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local",
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Create(ctx, &fleetv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("prints a manifest and bundledeployment", func() {
			buf, errBuf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(errBuf.Contents())).To(ContainSubstring("- Bundle:"), "cannot list inputs")
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: Content"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
			Expect(buf).To(gbytes.Say("---"))
			Expect(buf).To(gbytes.Say("kind: BundleDeployment"))
		})
	})
})
