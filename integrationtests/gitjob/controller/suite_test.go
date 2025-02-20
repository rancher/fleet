package controller

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	gomegatypes "github.com/onsi/gomega/types"
	"github.com/reugn/go-quartz/quartz"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	ctrlreconciler "github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/manifest"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git/mocks"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	timeout = 30 * time.Second
)

var (
	cfg            *rest.Config
	testEnv        *envtest.Environment
	ctx            context.Context
	cancel         context.CancelFunc
	k8sClient      client.Client
	logsBuffer     bytes.Buffer
	namespace      string
	expectedCommit string
	k8sClientSet   *kubernetes.Clientset
)

func TestGitJobController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet GitJob Controller Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClientSet, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	ctlr := gomock.NewController(GinkgoT())

	// redirect logs to a buffer that we can read in the tests
	GinkgoWriter.TeeTo(&logsBuffer)
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// return whatever commit the test is expecting
	fetcherMock := mocks.NewMockGitFetcher(ctlr)
	fetcherMock.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error) {
			return expectedCommit, nil
		},
	)

	// fleet-controller deployment
	err = k8sClient.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.ManagerConfigName,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "fleet-controller",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "fleet-controller",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "test", // value is required. but we don't need a real deployment for the test

						},
					},
				},
			},
		},
	})
	Expect(err).ToNot(HaveOccurred())

	sched := quartz.NewStdScheduler()
	Expect(sched).ToNot(BeNil())

	config.Set(&config.Config{})

	err = (&reconciler.GitJobReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Image:           "image",
		Scheduler:       sched,
		GitFetcher:      fetcherMock,
		Clock:           reconciler.RealClock{},
		Recorder:        mgr.GetEventRecorderFor("gitjob-controller"),
		Workers:         50,
		SystemNamespace: "default",
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	err = (&reconciler.StatusReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	store := manifest.NewStore(mgr.GetClient())
	builder := target.New(mgr.GetClient())
	err = (&ctrlreconciler.BundleReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Builder: builder,
		Store:   store,
		Query:   builder,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	go func() {
		defer GinkgoRecover()
		defer ctlr.Finish()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

func simulateIncreaseForceSyncGeneration(gitRepo v1alpha1.GitRepo) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		if err != nil {
			return err
		}
		gitRepoFromCluster.Spec.ForceSyncGeneration++
		return k8sClient.Update(ctx, &gitRepoFromCluster)
	})
}

func setGitRepoWebhookCommit(gitRepo v1alpha1.GitRepo, commit string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		if err != nil {
			return err
		}
		gitRepoFromCluster.Status.WebhookCommit = commit
		return k8sClient.Status().Update(ctx, &gitRepoFromCluster)
	})
}

func createGitRepo(gitRepoName string) v1alpha1.GitRepo {
	return v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitRepoName,
			Namespace: gitRepoNamespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo: repo,
		},
	}
}

func createGitRepoWithDisablePolling(gitRepoName string) v1alpha1.GitRepo {
	return v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gitRepoName,
			Namespace: gitRepoNamespace,
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:           repo,
			DisablePolling: true,
			Branch:         stableCommitBranch,
		},
	}
}

func waitDeleteGitrepo(gitRepo v1alpha1.GitRepo) {
	err := k8sClient.Delete(ctx, &gitRepo)
	Expect(err).ToNot(HaveOccurred())
	Eventually(func() bool {
		var gitRepoFromCluster v1alpha1.GitRepo
		err := k8sClient.Get(ctx, types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, &gitRepoFromCluster)
		return errors.IsNotFound(err)
	}).Should(BeTrue())
}

func beOwnedBy(expected interface{}) gomegatypes.GomegaMatcher {
	return gcustom.MakeMatcher(func(meta metav1.ObjectMeta) (bool, error) {
		ref, ok := expected.(metav1.OwnerReference)
		if !ok {
			return false, fmt.Errorf("beOwnedBy matcher expects metav1.OwnerReference")
		}

		for _, or := range meta.OwnerReferences {
			if or.Kind == ref.Kind && or.APIVersion == ref.APIVersion && or.Name == ref.Name {
				return true, nil
			}
		}

		return false, nil
	}).WithTemplate(
		"Expected:\n{{.FormattedActual}}\n{{.To}} contain owner reference " +
			"matching Kind, APIVersion and Name of \n{{format .Data 1}}",
	).WithTemplateData(expected)
}
