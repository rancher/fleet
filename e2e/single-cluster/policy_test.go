package singlecluster_test

// Tests for Policy CRD enforcement across GitRepo and HelmOp reconcilers.
// Runs against the single-cluster (fleet-local) setup.
//
// All fleet resources (GitRepo, HelmOp, Policy, SA) are created in env.Namespace
// (fleet-local), the fleet workspace namespace that the controller watches.
// Each test gets its own targetNamespace for deployed workloads.
//
// Covered scenarios:
//  1. No Policy → GitRepo reconciles normally (Accepted=True).
//  2. Policy requireServiceAccount=true, GitRepo no SA → Accepted=False.
//  3. Policy requireServiceAccount=true, GitRepo SA set → Accepted=True.
//  4. Policy allowedServiceAccounts set, GitRepo uses unlisted SA → Accepted=False.
//  5. Policy gitRepo.defaultServiceAccount → SA injected before check → Accepted=True.
//  6. Policy requireServiceAccount=true, HelmOp no SA → Accepted=False.
//  7. Policy helmOp.allowedHelmSecretNames, HelmOp uses unlisted secret → Accepted=False.
//  8. Multiple Policies: OR semantics for requireServiceAccount.
//  9. Bundle: direct fleet-apply bypass is blocked by Policy.

import (
	"context"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Policy enforcement", Label("policy"), func() {
	// k points at the fleet workspace namespace (fleet-local).
	// All fleet resources (GitRepo, HelmOp, Policy, SA) live here.
	// Each test also gets its own targetNamespace for deployed workloads.
	var (
		k               kubectl.Command
		targetNamespace string
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		targetNamespace = testenv.NewNamespaceName("policy-tgt", r)

		out, err := k.Create("ns", targetNamespace)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		_, _ = k.Delete("ns", targetNamespace, "--wait=false")
	})

	// createPolicy creates a Policy in the fleet workspace namespace.
	createPolicy := func(name string, spec fleet.Policy) {
		pol := &fleet.Policy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: env.Namespace,
			},
			RequireServiceAccount:  spec.RequireServiceAccount,
			AllowedServiceAccounts: spec.AllowedServiceAccounts,
			GitRepo:                spec.GitRepo,
			HelmOp:                 spec.HelmOp,
		}
		Expect(clientUpstream.Create(context.Background(), pol)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(context.Background(), pol)
		})
	}

	// createSA creates a ServiceAccount in the fleet workspace namespace.
	createSA := func(name string) {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: env.Namespace},
		}
		Expect(clientUpstream.Create(context.Background(), sa)).To(Succeed())
		DeferCleanup(func() {
			_ = clientUpstream.Delete(context.Background(), sa)
		})
	}

	// gitRepoCondition polls the Accepted condition of a GitRepo in the fleet workspace.
	gitRepoCondition := func(name string) func(g Gomega) string {
		return func(g Gomega) string {
			gr := &fleet.GitRepo{}
			g.Expect(clientUpstream.Get(context.Background(), types.NamespacedName{
				Name:      name,
				Namespace: env.Namespace,
			}, gr)).To(Succeed())
			for _, c := range gr.Status.Conditions {
				if c.Type == "Accepted" {
					return string(c.Status) + ":" + c.Message
				}
			}
			return "no-condition"
		}
	}

	// helmOpCondition polls the Accepted condition of a HelmOp in the fleet workspace.
	helmOpCondition := func(name string) func(g Gomega) string {
		return func(g Gomega) string {
			ho := &fleet.HelmOp{}
			g.Expect(clientUpstream.Get(context.Background(), types.NamespacedName{
				Name:      name,
				Namespace: env.Namespace,
			}, ho)).To(Succeed())
			for _, c := range ho.Status.Conditions {
				if c.Type == fleet.HelmOpAcceptedCondition {
					return string(c.Status) + ":" + c.Message
				}
			}
			return "no-condition"
		}
	}

	// applyGitRepo creates a GitRepo (no SA) in the fleet workspace namespace.
	applyGitRepo := func(name string) {
		err := testenv.ApplyTemplate(
			k,
			testenv.AssetPath("gitrepo-template.yaml"),
			testenv.GitRepoData{
				Name:            name,
				Branch:          "master",
				Paths:           []string{"simple"},
				TargetNamespace: targetNamespace,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = k.Delete("gitrepo", name)
		})
	}

	// applyGitRepoWithSA creates a GitRepo with a specific serviceAccount.
	applyGitRepoWithSA := func(name, sa string) {
		err := testenv.ApplyTemplate(
			k,
			testenv.AssetPath("policy/gitrepo-with-sa.yaml"),
			map[string]string{
				"Name":            name,
				"TargetNamespace": targetNamespace,
				"ServiceAccount":  sa,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = k.Delete("gitrepo", name)
		})
	}

	// -------------------------------------------------------------------------
	// GitRepo tests
	// -------------------------------------------------------------------------

	Describe("GitRepo reconciler", func() {
		Context("when no Policy exists in the namespace", func() {
			It("reconciles normally (Accepted=True)", func() {
				name := testenv.NewNamespaceName("gr", r)
				applyGitRepo(name)

				Eventually(gitRepoCondition(name), 30*time.Second, time.Second).
					Should(HavePrefix("True:"))
			})
		})

		Context("when Policy requireServiceAccount=true", func() {
			BeforeEach(func() {
				createPolicy("policy-rsa-"+testenv.NewNamespaceName("", r), fleet.Policy{RequireServiceAccount: true})
			})

			It("rejects GitRepo with no SA (Accepted=False)", func() {
				name := testenv.NewNamespaceName("gr", r)
				applyGitRepo(name)

				Eventually(gitRepoCondition(name), 30*time.Second, time.Second).Should(And(
					HavePrefix("False:"),
					ContainSubstring("serviceAccount"),
				))
			})

			It("accepts GitRepo with SA set (Accepted=True)", func() {
				sa := testenv.NewNamespaceName("sa", r)
				createSA(sa)
				name := testenv.NewNamespaceName("gr", r)
				applyGitRepoWithSA(name, sa)

				Eventually(gitRepoCondition(name), 30*time.Second, time.Second).Should(HavePrefix("True:"))
			})
		})

		Context("when Policy allowedServiceAccounts lists specific SAs", func() {
			BeforeEach(func() {
				createPolicy("policy-asa-"+testenv.NewNamespaceName("", r), fleet.Policy{
					AllowedServiceAccounts: []string{"approved-sa"},
				})
			})

			It("rejects GitRepo using an unlisted SA (Accepted=False)", func() {
				saName := testenv.NewNamespaceName("unapproved", r)
				createSA(saName)
				name := testenv.NewNamespaceName("gr", r)
				applyGitRepoWithSA(name, saName)

				Eventually(gitRepoCondition(name), 30*time.Second, time.Second).Should(And(
					HavePrefix("False:"),
					ContainSubstring("disallowed serviceAccount"),
				))
			})
		})

		Context("when Policy gitRepo.defaultServiceAccount is set", func() {
			var injectedSA string

			BeforeEach(func() {
				injectedSA = testenv.NewNamespaceName("injected-sa", r)
				createSA(injectedSA)
				createPolicy("policy-dsa-"+testenv.NewNamespaceName("", r), fleet.Policy{
					RequireServiceAccount: true,
					GitRepo: &fleet.GitRepoPolicySpec{
						DefaultServiceAccount: injectedSA,
					},
				})
			})

			It("injects the default SA and accepts the GitRepo (Accepted=True)", func() {
				name := testenv.NewNamespaceName("gr", r)
				// GitRepo created without an explicit SA.
				applyGitRepo(name)

				// Accepted because the default SA was injected before the check.
				Eventually(gitRepoCondition(name), 30*time.Second, time.Second).Should(HavePrefix("True:"))

				// The SA must have been written back onto the GitRepo spec.
				gr := &fleet.GitRepo{}
				Expect(clientUpstream.Get(context.Background(), types.NamespacedName{
					Name:      name,
					Namespace: env.Namespace,
				}, gr)).To(Succeed())
				Expect(gr.Spec.ServiceAccount).To(Equal(injectedSA))
			})
		})
	})

	// -------------------------------------------------------------------------
	// HelmOp tests — rejection cases
	// These fire before any chart fetch and need no real registry.
	// -------------------------------------------------------------------------

	// createHelmOpForRejection creates a HelmOp that will be rejected by Policy
	// before the reconciler ever tries to fetch the chart.
	createHelmOpForRejection := func(name, sa, secretName string) {
		err := testenv.ApplyTemplate(
			k,
			testenv.AssetPath("policy/helmop-minimal.yaml"),
			map[string]string{
				"Name":           name,
				"Namespace":      env.Namespace,
				"ServiceAccount": sa,
				"HelmSecretName": secretName,
				// Placeholder values — Policy rejection fires before any chart fetch.
				"Repo":    "https://charts.example.com",
				"Chart":   "placeholder",
				"Version": "0.1.0",
			},
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = k.Delete("helmop", name)
		})
	}

	Describe("HelmOp reconciler — rejection cases", func() {
		Context("when Policy requireServiceAccount=true", func() {
			BeforeEach(func() {
				createPolicy("policy-ho-rsa-"+testenv.NewNamespaceName("", r), fleet.Policy{RequireServiceAccount: true})
			})

			It("rejects HelmOp with no SA (Accepted=False)", func() {
				name := testenv.NewNamespaceName("ho", r)
				createHelmOpForRejection(name, "", "")

				Eventually(helmOpCondition(name), 30*time.Second, time.Second).Should(And(
					HavePrefix("False:"),
					ContainSubstring("serviceAccount"),
				))
			})
		})

		Context("when Policy helmOp.allowedHelmSecretNames is set", func() {
			BeforeEach(func() {
				createPolicy("policy-ho-ahn-"+testenv.NewNamespaceName("", r), fleet.Policy{
					HelmOp: &fleet.HelmOpPolicySpec{
						AllowedHelmSecretNames: []string{"approved-secret"},
					},
				})
			})

			It("rejects HelmOp referencing an unlisted secret (Accepted=False)", func() {
				name := testenv.NewNamespaceName("ho", r)
				createHelmOpForRejection(name, "", "unapproved-secret")

				Eventually(helmOpCondition(name), 30*time.Second, time.Second).Should(And(
					HavePrefix("False:"),
					ContainSubstring("disallowed helmSecretName"),
				))
			})
		})
	})

	// -------------------------------------------------------------------------
	// Multiple-policy OR semantics
	// -------------------------------------------------------------------------

	Describe("multiple Policies in a namespace", func() {
		It("OR-s requireServiceAccount: one strict Policy is enough to enforce", func() {
			createPolicy("policy-or-perm-"+testenv.NewNamespaceName("", r), fleet.Policy{RequireServiceAccount: false})
			createPolicy("policy-or-strict-"+testenv.NewNamespaceName("", r), fleet.Policy{RequireServiceAccount: true})

			name := testenv.NewNamespaceName("gr", r)
			applyGitRepo(name)

			Eventually(gitRepoCondition(name), 30*time.Second, time.Second).Should(And(
				HavePrefix("False:"),
				ContainSubstring("serviceAccount"),
			))
		})
	})

	// -------------------------------------------------------------------------
	// Bundle — direct fleet-apply bypass
	// -------------------------------------------------------------------------

	// bundleReadyCondition polls the Ready condition of a Bundle in the fleet workspace.
	bundleReadyCondition := func(name string) func(g Gomega) string {
		return func(g Gomega) string {
			b := &fleet.Bundle{}
			g.Expect(clientUpstream.Get(context.Background(), types.NamespacedName{
				Name:      name,
				Namespace: env.Namespace,
			}, b)).To(Succeed())
			for _, c := range b.Status.Conditions {
				if c.Type == string(fleet.Ready) {
					return string(c.Status) + ":" + c.Message
				}
			}
			return "no-condition"
		}
	}

	Describe("Bundle reconciler", func() {
		Context("when Policy requireServiceAccount=true", func() {
			BeforeEach(func() {
				createPolicy("policy-bundle-rsa-"+testenv.NewNamespaceName("", r), fleet.Policy{RequireServiceAccount: true})
			})

			It("rejects a directly-created Bundle with no SA (Ready=False)", func() {
				name := testenv.NewNamespaceName("bundle", r)
				bundle := &fleet.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: env.Namespace,
					},
					Spec: fleet.BundleSpec{
						Resources: []fleet.BundleResource{
							{Name: "configmap.yaml", Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n"},
						},
						Targets: []fleet.BundleTarget{
							{Name: "default", ClusterGroup: "default"},
						},
					},
				}
				Expect(clientUpstream.Create(context.Background(), bundle)).To(Succeed())
				DeferCleanup(func() {
					_ = clientUpstream.Delete(context.Background(), bundle)
				})

				Eventually(bundleReadyCondition(name), 30*time.Second, time.Second).Should(And(
					HavePrefix("False:"),
					ContainSubstring("serviceAccount"),
				))
			})
		})
	})
})
