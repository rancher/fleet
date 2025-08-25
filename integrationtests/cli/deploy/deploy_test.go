package deploy_test

import (
	"github.com/onsi/gomega/gbytes"

	clihelper "github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/cli"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Fleet CLI Deploy", func() {
	var args []string

	act := func(args []string) (*gbytes.Buffer, error) {
		cmd := cli.NewDeploy()
		args = append([]string{"--kubeconfig", kubeconfigPath}, args...)
		cmd.SetArgs(args)

		buf := gbytes.NewBuffer()
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		err := cmd.Execute()
		return buf, err
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
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("Usage:"))
		})
	})

	When("Input file is missing", func() {
		BeforeEach(func() {
			args = []string{"--input-file", "/tmp/does-not-exist-bundle.yaml"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("no such file or directory"))
		})
	})

	When("Input file is invalid", func() {
		BeforeEach(func() {
			args = []string{"--input-file", clihelper.AssetsPath + "helmrepository/config-chart-0.1.0.tgz"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("yaml: control characters are not allowed"))
		})
	})

	When("Input file does not contain a content resource", func() {
		BeforeEach(func() {
			args = []string{"--input-file", clihelper.AssetsPath + "bundledeployment/bd-only.yaml"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("failed to read content resource from file"))
		})
	})

	When("Input file does not contain a bundledeployment resource", func() {
		BeforeEach(func() {
			args = []string{"--input-file", clihelper.AssetsPath + "bundledeployment/content.yaml"}
		})

		It("prints an error", func() {
			errBuf, err := act(args)
			Expect(err).To(HaveOccurred())
			Expect(errBuf).To(gbytes.Say("failed to read bundledeployment"))
		})
	})

	When("Deploying to a cluster", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
			}
		})

		It("creates resources", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("Specifying a namespace", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--namespace", namespace,
			}
		})

		It("creates resources", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("deploying on top of a release in `pending-install` status", func() {
		BeforeEach(func() {
			releaseSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sh.helm.release.v1.testbundle-simple-chart.v1",
					Namespace: "default",
					Labels: map[string]string{
						"name":    "testbundle-simple-chart",
						"owner":   "helm",
						"status":  "pending-install",
						"version": "1",
					},
				},
				Type: "helm.sh/release.v1",
				Data: map[string][]byte{
					"release": []byte("H4sIAAAAAAAAA51V23LiOBD9FZdfF4htAgFXzUNgA8vkMpUbBG9SKVlu2wqypLJkByeVfx/JhpBkZpLafTJq+pzuPupWP9sMZWD7tgKpwoJFFNqSZEJ/cIpyZbdswmJu+892THKp7iMQlFcQaYjneL22M2h7vSu35ztDf/+g47ruwO0O+4O/HM93HA2n6P+gIqCgav/6IHFOhCKcacOMSYUotTA3aSrQDtqgCqn/E8AiwpI2aXzsl5bdlKHzz0ChCClkfm+KxpzFJHmttIRcNjGcjttxfol8aBmVrNrdUilSlkaQmIC0iJJWw6ZRSJD5K1Xp1Raxs7gdt1+zI8a4zlxbZS0wBVAdjJSi0CF8DyXAVNukKgXC0Gjx0WlzZyT69A4/olYAop2D5EWOwQgXIyrhN44S8pJgaCOMecGUyUFrqiph0tFVUYLrAoyVcryyfVZQqj1Ax0fKcP/7vOuxjXGvkSpDolOhjBqh64uxlzcjMc/mFfZoGT7wJHo4OkYeLYK/eXLuDYsgo+xqMXkcZy6NppPV8uYi/ZHwZDbtpeHiuj/754Li7rnSeIWna3qyOOPLm++O/pZhFojgUewwjW9/Nv7uLRdrN7g6qk6r2fFsPCqWC5f+IKMDqA6LeTaR0WL+dJLVMZL4xjnW5f6mqo1YG63+Q2nX3ryKMvoQXE8elt7QDdn5cbiYO8vFRRpNj/pjcpgsF70i7JqzxrPT2qZT5cFirU7YGT/x0jL0pJZgVAWXrgjZmcav5cl49IizaxXdjBjOJqvg3Mi1TpfZXOInrsvVPNOgDJ94sswmleENs4kKrniCvaHSXCWezistW4mTb9/slzs9KYgWIN9MUgQxKmjTraYXJE4hQ9tuiAk13uZgJrKZkx0Y1qju1prVoDPESKybue6x7Sz5VuneMqOmf8ssy2B96x3UmM0MaE9zdL3uLVsRFvnWuA55isQt2z4DNcmbCazPlsXDB8BK6iHICX8zCESzbIv8w5wZAopCoF9xpUimvtXFqDtEUdRznGEY9feH+14Y457rDcIDFEM4QIOugz20K9bEfRex3Uh5y3SbpZyv3k2b5Bm0uYAcKZ5rB6OENl82TXq4GeiWLZBKP7yFe1/19CcX1Aj+PsqXqqdAs45M90wRviVy2L7grfpQiCRHEfwicMy5b4Uo30n0sWgo9RtqZLHfkJqid6z23WZJ5QUzTaldcr167pH6w7bq9p39QW/gdV+31XYVfQoa9txufzB8BQndBuaeLguMASK97F62e+9ecP2wkvr1tEOIuc7WSNPGOTTP7V09hduV4rbst2ti06f2y09Y3Y4W3gcAAA=="),
				},
			}
			Expect(k8sClient.Create(ctx, &releaseSecret)).ToNot(HaveOccurred())
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--namespace", "default",
			}
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, &releaseSecret)).ToNot(HaveOccurred())
			})
		})

		It("installs the release successfully", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())

			By("creating resources")
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	When("Printing results with --dry-run", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd.yaml",
				"--dry-run",
			}
		})

		It("prints a manifest and bundledeployment", func() {
			buf, err := act(args)
			Expect(err).NotTo(HaveOccurred())
			Expect(buf.Contents()).To(And(
				ContainSubstring("- apiVersion: v1"),
				ContainSubstring("ConfigMap"),
				ContainSubstring("  data:"),
				ContainSubstring("    name: example-value"),
				ContainSubstring("ServiceAccount"),
				ContainSubstring("helm.sh/hook"),
				ContainSubstring("some-operator"),
			))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("Printing results with --dry-run where the chart specifies kubeVersion >= 1.26.0", func() {
		BeforeEach(func() {
			args = []string{
				"--input-file", clihelper.AssetsPath + "bundledeployment/bd-with-kube-version.yaml",
				"--dry-run",
				"--kube-version", "v1.27.0",
			}
		})

		It("prints a manifest and bundledeployment", func() {
			buf, err := act(args)
			Expect(err).ToNot(HaveOccurred())
			Expect(buf).To(gbytes.Say("- apiVersion: v1"))
			Expect(buf).To(gbytes.Say("  data:"))
			Expect(buf).To(gbytes.Say("    name: example-value"))

			cm := &corev1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, cm)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
