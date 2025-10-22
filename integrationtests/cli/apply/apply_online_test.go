package apply

import (
	"context"
	"time"

	"go.uber.org/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Fleet apply online", Label("online"), func() {

	var (
		ctrl       *gomock.Controller
		clientMock *mocks.MockK8sClient
		name       string
		dirs       []string
		options    apply.Options
		oldBundle  *fleet.Bundle
		newBundle  *fleet.Bundle
	)

	JustBeforeEach(func() {
		//Setting up all the needed mocked interfaces for the test
		ctrl = gomock.NewController(GinkgoT())
		clientMock = mocks.NewMockK8sClient(ctrl)
		clientMock.EXPECT().Get(
			gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleet.Bundle{}),
		).DoAndReturn(
			func(_ context.Context, ns types.NamespacedName, bundle *fleet.Bundle, _ ...interface{}) error {
				bundle.ObjectMeta = oldBundle.ObjectMeta
				bundle.Name = ns.Name
				bundle.Namespace = ns.Namespace
				bundle.Spec = oldBundle.Spec
				return nil
			},
		).AnyTimes()
		clientMock.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		clientMock.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		// so it does not try to use OCI storage
		clientMock.EXPECT().Get(
			gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Secret{}),
		).Return(errors.NewNotFound(schema.GroupResource{}, "")).AnyTimes()
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

	When("A HelmOps bundle already exists in the same namespace", func() {
		BeforeEach(func() {
			name = "labels_update"
			dirs = []string{cli.AssetsPath + "labels_update"}
			//bundle in the cluster
			oldBundle = &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "labels_update",
				},
				Spec: fleet.BundleSpec{
					HelmOpOptions: &fleet.BundleHelmOptions{
						// values themselves do not matter, as long as Helm options are non-null and the bundle is therefore detected as
						// a HelmOps bundle.
						SecretName: "foo",
					},
				},
			}
		})

		It("detects the existing bundle and fails to create the new bundle", func() {
			// No update expected here, as checks for existence of an existing bundle should reveal that a
			// HelmOps bundle with the same name already exists.

			err := fleetApplyOnline(clientMock, name, dirs, options)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already exists"))
		})
	})

	When("a bundle to be created already exists and is marked for deletion", func() {
		BeforeEach(func() {
			ts := metav1.NewTime(time.Now())
			name = "labels_update"
			dirs = []string{cli.AssetsPath + "labels_update"}
			//bundle in the cluster
			oldBundle = &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "foo",
					Name:              "test_labels",
					DeletionTimestamp: &ts, // as long as it's non-nil
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

		It("does not update the bundle", func() {
			// no expected call to update nor updateStatus, as the existing bundle is being deleted
			err := fleetApplyOnline(clientMock, name, dirs, options)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("is being deleted"))
		})

		It("does not update an OCI bundle", func() {
			// no expected call to update nor updateStatus, as the existing bundle is being deleted
			bkp := options.OCIRegistry.Reference
			options.OCIRegistry.Reference = "oci://foo" // non-empty

			defer func() { options.OCIRegistry.Reference = bkp }()

			err := fleetApplyOnline(clientMock, name, dirs, options)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("is being deleted"))
		})

	})
})
