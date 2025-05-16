//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/getter_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/cli/apply Getter
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/fleet_controller_mock.go -package=mocks -mock_names=Interface=FleetInterface github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1 Interface
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/core_controller_mock.go -package=mocks -mock_names=Interface=CoreInterface github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1 Interface

package apply

import (
	"context"

	"go.uber.org/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Fleet apply online", Label("online"), func() {

	var (
		ctrl       *gomock.Controller
		clientMock *mocks.MockClient
		name       string
		dirs       []string
		options    apply.Options
		oldBundle  *fleet.Bundle
		newBundle  *fleet.Bundle
	)

	JustBeforeEach(func() {
		//Setting up all the needed mocked interfaces for the test
		ctrl = gomock.NewController(GinkgoT())
		clientMock = mocks.NewMockClient(ctrl)
		clientMock.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, ns types.NamespacedName, bundle *fleet.Bundle, _ ...interface{}) error {
				bundle.ObjectMeta = oldBundle.ObjectMeta
				bundle.Name = ns.Name
				bundle.Namespace = ns.Namespace
				return nil
			},
		)
		clientMock.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		clientMock.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	})

	When("We want to delete a label in the bundle from the cluster", func() {
		BeforeEach(func() {
			name = "labels_update"
			dirs = []string{cli.AssetsPath + "labels_update"}
			//bundle in the cluster
			oldBundle = &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"new":         "fleet-label2",
						"new_changed": "fleet_label_changed",
					},
					Namespace: "foo",
					Name:      "test_labels",
				},
			}
			// bundle in the cm.yaml file; some values are autofilled in the implementation (this is why there are not only the labels)
			newBundle = &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"new": "fleet-label2",
					},
					Namespace: "foo",
					Name:      "test_labels",
				},
				Spec: fleet.BundleSpec{
					Resources: []fleet.BundleResource{
						{
							Name: "cm.yaml",
							Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm3
data:
  test: "value23"
`,
						},
					},
					Targets: []fleet.BundleTarget{
						{
							Name:         "default",
							ClusterGroup: "default",
						},
					},
				},
			}
		})

		It("deletes labels present on the bundle but not in fleet.yaml", func() {
			// Update is called with the actual output of the `apply` command here, hence we validate that its argument is what we expect.
			clientMock.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, bundle *fleet.Bundle, _ ...interface{}) error {
					Expect(bundle.Spec).To(Equal(newBundle.Spec))
					Expect(bundle.Labels).To(Equal(newBundle.Labels))
					return nil
				},
			)
			err := fleetApplyOnline(clientMock, name, dirs, options)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
