package reconciler

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("ContentReconciler", func() {
	var (
		ctx     context.Context
		sch     *runtime.Scheme
		cl      client.Client
		r       *ContentReconciler
		content *fleet.Content
	)

	BeforeEach(func() {
		ctx = context.Background()
		sch = runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(sch)).To(Succeed())
		Expect(fleet.AddToScheme(sch)).To(Succeed())
	})

	Describe("mapBundleDeploymentToContent", func() {
		It("returns nil for non-BundleDeployment objects", func() {
			reconciler := &ContentReconciler{}
			Expect(reconciler.mapBundleDeploymentToContent(ctx, &fleet.Content{})).To(BeNil())
		})

		It("returns nil for BundleDeployment without content label", func() {
			reconciler := &ContentReconciler{}
			bd := &fleet.BundleDeployment{}
			Expect(reconciler.mapBundleDeploymentToContent(ctx, bd)).To(BeNil())
		})

		It("returns nil for BundleDeployment with a label using an empty name", func() {
			reconciler := &ContentReconciler{}
			bd := &fleet.BundleDeployment{}
			bd.Labels = map[string]string{fleet.ContentNameLabel: ""}
			Expect(reconciler.mapBundleDeploymentToContent(ctx, bd)).To(BeNil())
		})

		It("maps BundleDeployment with label to a single content request", func() {
			reconciler := &ContentReconciler{}
			bd := &fleet.BundleDeployment{}
			name := "my-content"
			bd.Labels = map[string]string{fleet.ContentNameLabel: name}
			res := reconciler.mapBundleDeploymentToContent(ctx, bd)
			Expect(res).To(HaveLen(1))
			Expect(res[0].NamespacedName.Name).To(Equal(name))
			Expect(res[0].NamespacedName.Namespace).To(Equal(""))
		})
	})

	Describe("Reconcile", func() {
		JustBeforeEach(func() {
			// default fake client built by each test case as needed
			r = &ContentReconciler{Client: cl, Scheme: sch}
		})

		Context("when Content is not present", func() {
			BeforeEach(func() {
				cl = fake.NewClientBuilder().WithScheme(sch).Build()
			})

			It("should not return an error for a non-existent Content", func() {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: "does-not-exist"}})
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("when Content has no referencing BundleDeployments", func() {
			BeforeEach(func() {
				content = &fleet.Content{
					ObjectMeta: metav1.ObjectMeta{Name: "content-to-delete"},
					Status:     fleet.ContentStatus{ReferenceCount: 1},
				}
				cl = fake.NewClientBuilder().WithScheme(sch).
					WithIndex(&fleet.BundleDeployment{}, config.ContentNameIndex, func(obj client.Object) []string {
						bd, ok := obj.(*fleet.BundleDeployment)
						if !ok {
							return nil
						}
						if val, exists := bd.Labels[fleet.ContentNameLabel]; exists {
							return []string{val}
						}
						return nil
					}).
					WithObjects(content).
					Build()
			})

			It("deletes the content when there are no non-deleted BundleDeployments", func() {
				// ensure content exists before reconcile
				pre := &fleet.Content{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: content.Name}, pre)).To(Succeed())

				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				// content should be gone
				got := &fleet.ContentList{}
				Expect(cl.List(ctx, got)).To(Succeed())
				Expect(got.Items).To(BeEmpty())
			})
		})

		Context("when there are referencing BundleDeployments", func() {
			BeforeEach(func() {
				content = &fleet.Content{
					ObjectMeta: metav1.ObjectMeta{Name: "content-1"},
					Status:     fleet.ContentStatus{ReferenceCount: 0},
				}

				bd := &fleet.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bd-1",
						Namespace: "default",
						Labels:    map[string]string{fleet.ContentNameLabel: content.Name},
					},
				}

				cl = fake.NewClientBuilder().WithScheme(sch).
					WithIndex(&fleet.BundleDeployment{}, config.ContentNameIndex, func(obj client.Object) []string {
						bd, ok := obj.(*fleet.BundleDeployment)
						if !ok {
							return nil
						}
						if val, exists := bd.Labels[fleet.ContentNameLabel]; exists {
							return []string{val}
						}
						return nil
					}).
					WithObjects(content, bd).
					WithStatusSubresource(&fleet.Content{}).
					Build()
			})

			It("updates the Content reference count to match non-deleted BundleDeployments", func() {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				gotList := &fleet.ContentList{}
				Expect(cl.List(ctx, gotList)).To(Succeed())
				Expect(gotList.Items).To(HaveLen(1))
				Expect(gotList.Items[0].Status.ReferenceCount).To(Equal(1))

				// re-run reconcile and ensure the count remains the same
				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				gotList2 := &fleet.ContentList{}
				Expect(cl.List(ctx, gotList2)).To(Succeed())
				Expect(gotList2.Items).To(HaveLen(1))
				Expect(gotList2.Items[0].Status.ReferenceCount).To(Equal(1))
			})
		})

		Context("when BundleDeployments referencing the content are marked for deletion", func() {
			BeforeEach(func() {
				content = &fleet.Content{
					ObjectMeta: metav1.ObjectMeta{Name: "content-2"},
					Status:     fleet.ContentStatus{ReferenceCount: 0},
				}

				deletionTime := metav1.NewTime(time.Now())
				bd := &fleet.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "bd-deleted",
						Namespace:         "default",
						Labels:            map[string]string{fleet.ContentNameLabel: content.Name},
						DeletionTimestamp: &deletionTime,
						Finalizers:        []string{"test.finalizer"},
					},
				}

				cl = fake.NewClientBuilder().WithScheme(sch).
					WithIndex(&fleet.BundleDeployment{}, config.ContentNameIndex, func(obj client.Object) []string {
						bd, ok := obj.(*fleet.BundleDeployment)
						if !ok {
							return nil
						}
						if val, exists := bd.Labels[fleet.ContentNameLabel]; exists {
							return []string{val}
						}
						return nil
					}).
					WithObjects(content, bd).
					WithStatusSubresource(&fleet.Content{}).
					Build()
			})

			It("ignores deleted BundleDeployments when counting references", func() {
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				gotList := &fleet.ContentList{}
				Expect(cl.List(ctx, gotList)).To(Succeed())
				Expect(gotList.Items).To(HaveLen(1))
				Expect(gotList.Items[0].Status.ReferenceCount).To(Equal(0))
			})
		})

		Context("when Content has finalizers but no BundleDeployments reference it", func() {
			BeforeEach(func() {
				content = &fleet.Content{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "content-with-finalizers",
						Finalizers: []string{"fleet.cattle.io/test-finalizer"},
					},
					Status: fleet.ContentStatus{ReferenceCount: 0},
				}

				cl = fake.NewClientBuilder().WithScheme(sch).
					WithIndex(&fleet.BundleDeployment{}, config.ContentNameIndex, func(obj client.Object) []string {
						bd, ok := obj.(*fleet.BundleDeployment)
						if !ok {
							return nil
						}
						if val, exists := bd.Labels[fleet.ContentNameLabel]; exists {
							return []string{val}
						}
						return nil
					}).
					WithObjects(content).
					Build()
			})

			It("removes finalizers and deletes the Content", func() {
				// ensure content exists with finalizers before reconcile
				pre := &fleet.Content{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: content.Name}, pre)).To(Succeed())
				Expect(pre.GetFinalizers()).To(HaveLen(1))

				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				// content should be deleted
				got := &fleet.ContentList{}
				Expect(cl.List(ctx, got)).To(Succeed())
				Expect(got.Items).To(BeEmpty())
			})
		})

		Context("when Content has finalizers and ReferenceCount > 0 but no BundleDeployments reference it", func() {
			BeforeEach(func() {
				content = &fleet.Content{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "content-with-finalizers-and-refs",
						Finalizers: []string{"fleet.cattle.io/test-finalizer"},
					},
					Status: fleet.ContentStatus{ReferenceCount: 2},
				}

				cl = fake.NewClientBuilder().WithScheme(sch).
					WithIndex(&fleet.BundleDeployment{}, config.ContentNameIndex, func(obj client.Object) []string {
						bd, ok := obj.(*fleet.BundleDeployment)
						if !ok {
							return nil
						}
						if val, exists := bd.Labels[fleet.ContentNameLabel]; exists {
							return []string{val}
						}
						return nil
					}).
					WithObjects(content).
					Build()
			})

			It("removes finalizers and deletes the Content", func() {
				// ensure content exists with finalizers before reconcile
				pre := &fleet.Content{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: content.Name}, pre)).To(Succeed())
				Expect(pre.GetFinalizers()).To(HaveLen(1))
				Expect(pre.Status.ReferenceCount).To(Equal(2))

				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				// content should be deleted
				got := &fleet.ContentList{}
				Expect(cl.List(ctx, got)).To(Succeed())
				Expect(got.Items).To(BeEmpty())
			})
		})

		Context("when Content has finalizers and active BundleDeployments", func() {
			BeforeEach(func() {
				content = &fleet.Content{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "content-with-finalizers-and-bds",
						Finalizers: []string{"fleet.cattle.io/test-finalizer"},
					},
					Status: fleet.ContentStatus{ReferenceCount: 0},
				}

				bd := &fleet.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bd-active",
						Namespace: "default",
						Labels:    map[string]string{fleet.ContentNameLabel: content.Name},
					},
				}

				cl = fake.NewClientBuilder().WithScheme(sch).
					WithIndex(&fleet.BundleDeployment{}, config.ContentNameIndex, func(obj client.Object) []string {
						bd, ok := obj.(*fleet.BundleDeployment)
						if !ok {
							return nil
						}
						if val, exists := bd.Labels[fleet.ContentNameLabel]; exists {
							return []string{val}
						}
						return nil
					}).
					WithObjects(content, bd).
					WithStatusSubresource(&fleet.Content{}).
					Build()
			})

			It("removes finalizers but does not delete the Content", func() {
				// ensure content exists with finalizers before reconcile
				pre := &fleet.Content{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: content.Name}, pre)).To(Succeed())
				Expect(pre.GetFinalizers()).To(HaveLen(1))

				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: content.Name}})
				Expect(err).ToNot(HaveOccurred())

				// content should still exist with updated reference count and no finalizers
				got := &fleet.Content{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: content.Name}, got)).To(Succeed())
				Expect(got.GetFinalizers()).To(BeEmpty())
				Expect(got.Status.ReferenceCount).To(Equal(1))
			})
		})
	})
})
