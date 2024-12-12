package resourcestatus

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/wrangler/v3/pkg/summary"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Resourcekey", func() {
	var (
		gitrepo *fleet.GitRepo
		list    *fleet.BundleDeploymentList
	)

	BeforeEach(func() {
		gitrepo = &fleet.GitRepo{
			Status: fleet.GitRepoStatus{
				Summary: fleet.BundleSummary{
					Ready:       2,
					WaitApplied: 1,
				},
			},
		}
		list = &fleet.BundleDeploymentList{
			Items: []fleet.BundleDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bd1",
						Labels: map[string]string{
							fleet.RepoLabel:             "gitrepo1",
							fleet.ClusterLabel:          "cluster1",
							fleet.ClusterNamespaceLabel: "c-ns1",
						},
					},
					Status: fleet.BundleDeploymentStatus{
						Resources: []fleet.BundleDeploymentResource{
							{
								Kind:       "Deployment",
								APIVersion: "v1",
								Name:       "web",
								Namespace:  "default",
							},
							{
								// extra service for one cluster
								Kind:       "Service",
								APIVersion: "v1",
								Name:       "web-svc",
								Namespace:  "default",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bd1",
						Labels: map[string]string{
							fleet.RepoLabel:             "gitrepo1",
							fleet.ClusterLabel:          "cluster2",
							fleet.ClusterNamespaceLabel: "c-ns1",
						},
					},
					Status: fleet.BundleDeploymentStatus{
						Resources: []fleet.BundleDeploymentResource{
							{
								Kind:       "Deployment",
								APIVersion: "v1",
								Name:       "web",
								Namespace:  "default",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "bd1",
						Labels: map[string]string{
							fleet.RepoLabel:             "gitrepo1",
							fleet.ClusterLabel:          "cluster1",
							fleet.ClusterNamespaceLabel: "c-ns2",
						},
					},
					Status: fleet.BundleDeploymentStatus{
						NonReadyStatus: []fleet.NonReadyStatus{
							{
								Kind:       "Deployment",
								APIVersion: "v1",
								Name:       "web",
								Namespace:  "default",
								Summary: summary.Summary{
									State:         "Pending",
									Error:         true,
									Transitioning: true,
									Message:       []string{"message1", "message2"},
								},
							},
						},
						Resources: []fleet.BundleDeploymentResource{
							{
								Kind:       "Deployment",
								APIVersion: "v1",
								Name:       "web",
								Namespace:  "default",
							},
						},
					},
				},
			},
		}

	})

	It("returns a list", func() {
		SetGitRepoResources(list, gitrepo)

		Expect(gitrepo.Status.Resources).To(HaveLen(2))
		Expect(gitrepo.Status.Resources).To(ContainElement(fleet.GitRepoResource{
			APIVersion: "v1",
			Kind:       "Deployment",
			Type:       "deployment",
			ID:         "default/web",

			Namespace: "default",
			Name:      "web",

			IncompleteState: false,
			State:           "WaitApplied",
			Error:           false,
			Transitioning:   false,
			Message:         "",
			PerClusterState: []fleet.ResourcePerClusterState{
				{
					State:         "Pending",
					ClusterID:     "c-ns2/cluster1",
					Error:         true,
					Transitioning: true,
					Message:       "message1; message2",
					Patch:         nil,
				},
			},
		}))
		Expect(gitrepo.Status.Resources).To(ContainElement(fleet.GitRepoResource{
			APIVersion: "v1",
			Kind:       "Service",
			Type:       "service",
			ID:         "default/web-svc",

			Namespace: "default",
			Name:      "web-svc",

			IncompleteState: false,
			State:           "WaitApplied",
			Error:           false,
			Transitioning:   false,
			Message:         "",
			PerClusterState: []fleet.ResourcePerClusterState{},
		}))

		Expect(gitrepo.Status.ResourceErrors).To(BeEmpty())

		Expect(gitrepo.Status.ResourceCounts.Ready).To(Equal(0))
		Expect(gitrepo.Status.ResourceCounts.DesiredReady).To(Equal(4))
		Expect(gitrepo.Status.ResourceCounts.WaitApplied).To(Equal(3))
		Expect(gitrepo.Status.ResourceCounts.Modified).To(Equal(0))
		Expect(gitrepo.Status.ResourceCounts.Orphaned).To(Equal(0))
		Expect(gitrepo.Status.ResourceCounts.Missing).To(Equal(0))
		Expect(gitrepo.Status.ResourceCounts.Unknown).To(Equal(0))
		Expect(gitrepo.Status.ResourceCounts.NotReady).To(Equal(1))
	})
})
