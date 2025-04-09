//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/getter_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/cli/apply Getter
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/fleet_controller_mock.go -package=mocks -mock_names=Interface=FleetInterface github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1 Interface
//go:generate mockgen --build_flags=--mod=mod -destination=../../mocks/core_controller_mock.go -package=mocks -mock_names=Interface=CoreInterface github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1 Interface

package apply

import (
	"github.com/golang/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/integrationtests/mocks"
	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Fleet apply online", Label("online"), func() {

	var (
		ctrl             *gomock.Controller
		getter           *mocks.MockGetter
		c                client.Client
		fleetMock        *mocks.FleetInterface
		bundleController *fake.MockControllerInterface[*fleet.Bundle, *fleet.BundleList]
		name             string
		dirs             []string
		options          apply.Options
		oldBundle        *fleet.Bundle
		newBundle        *fleet.Bundle
	)

	JustBeforeEach(func() {
		//Setting up all the needed mocked interfaces for the test
		ctrl = gomock.NewController(GinkgoT())
		getter = mocks.NewMockGetter(ctrl)
		c = client.Client{
			Fleet:     mocks.NewFleetInterface(ctrl),
			Core:      mocks.NewCoreInterface(ctrl),
			Namespace: "foo",
		}
		bundleController = fake.NewMockControllerInterface[*fleet.Bundle, *fleet.BundleList](ctrl)
		secretController := fake.NewMockControllerInterface[*corev1.Secret, *corev1.SecretList](ctrl)
		fleetMock = c.Fleet.(*mocks.FleetInterface)
		coreMock := c.Core.(*mocks.CoreInterface)
		getter.EXPECT().GetNamespace().Return("foo").AnyTimes()
		getter.EXPECT().Get().Return(&c, nil).AnyTimes()
		fleetMock.EXPECT().Bundle().Return(bundleController).AnyTimes()
		coreMock.EXPECT().Secret().Return(secretController).AnyTimes()
		bundleController.EXPECT().Get("foo", gomock.Any(), gomock.Any()).Return(oldBundle, nil).AnyTimes()
		bundleController.EXPECT().List(gomock.Any(), gomock.Any()).Return(&fleet.BundleList{}, nil).AnyTimes()
		secretController.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
			bundleController.EXPECT().Update(newBundle).Return(newBundle, nil).AnyTimes()
			bundleController.EXPECT().UpdateStatus(newBundle).Return(newBundle, nil).AnyTimes()
			err := fleetApplyOnline(getter, name, dirs, options)
			Expect(err).NotTo(HaveOccurred())
		})

	})

})
