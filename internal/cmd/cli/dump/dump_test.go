package dump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_getNamespaces(t *testing.T) {
	objs := []runtime.Object{
		&v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns1",
			},
		},
		&v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster2",
				Namespace: "ns2",
			},
		},
		&v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster3",
				Namespace: "ns1", // Same namespace as cluster1
			},
		},
		&corev1.ConfigMap{ // should not have its namespace listed (not a cluster)
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns3",
			},
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	namespaces, err := getNamespaces(ctx, fakeDynClient, logger, Options{FetchLimit: 0})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedNS := map[string]struct{}{
		"default":                   {},
		"kube-system":               {},
		"cattle-fleet-system":       {},
		"cattle-fleet-local-system": {},
		"ns1":                       {},
		"ns2":                       {},
	}

	if len(namespaces) != len(expectedNS) {
		t.Fatalf("expected %d namespaces, got %d: %v", len(expectedNS), len(namespaces), namespaces)
	}

	// Check for duplicates
	seen := make(map[string]bool)
	for _, ns := range namespaces {
		if seen[ns] {
			t.Fatalf("namespace %s appears more than once in result", ns)
		}
		seen[ns] = true
	}

	for _, got := range namespaces {
		if _, ok := expectedNS[got]; !ok {
			t.Fatalf("got unexpected namespace %s", got)
		}
	}
}

func Test_getNamespaces_pagination(t *testing.T) {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	// Create a fake dynamic client with a scheme
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create clusters in different namespaces
	clusters := []*v1alpha1.Cluster{
		{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "Cluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns1",
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "Cluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster2",
				Namespace: "ns2",
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "Cluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster3",
				Namespace: "ns3",
			},
		},
	}

	// Convert to unstructured
	var objs []runtime.Object
	for _, cluster := range clusters {
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
		if err != nil {
			t.Fatalf("failed to convert to unstructured: %v", err)
		}
		objs = append(objs, &unstructured.Unstructured{Object: unstructuredObj})
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)

	// Set up pagination: first page returns 2 clusters, second page returns 1
	callCount := 0
	fakeDynClient.PrependReactor("list", "clusters", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion("fleet.cattle.io/v1alpha1")
		list.SetKind("ClusterList")

		callCount++
		switch callCount {
		case 1:
			// First page: return first 2 clusters with continue token
			list.SetResourceVersion("1")
			list.SetContinue("continue-token")
			list.Items = []unstructured.Unstructured{*objs[0].(*unstructured.Unstructured), *objs[1].(*unstructured.Unstructured)}
		case 2:
			// Second page: return last cluster
			list.SetResourceVersion("2")
			list.SetContinue("")
			list.Items = []unstructured.Unstructured{*objs[2].(*unstructured.Unstructured)}
		}

		return true, list, nil
	})

	// Test with fetchLimit = 2 (should trigger pagination)
	namespaces, err := getNamespaces(ctx, fakeDynClient, logger, Options{FetchLimit: 2})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify all namespaces are included
	expectedNS := map[string]struct{}{
		"default":                   {},
		"kube-system":               {},
		"cattle-fleet-system":       {},
		"cattle-fleet-local-system": {},
		"ns1":                       {},
		"ns2":                       {},
		"ns3":                       {},
	}

	if len(namespaces) != len(expectedNS) {
		t.Fatalf("expected %d namespaces, got %d: %v", len(expectedNS), len(namespaces), namespaces)
	}

	for _, got := range namespaces {
		if _, ok := expectedNS[got]; !ok {
			t.Fatalf("got unexpected namespace %s", got)
		}
	}

	// Verify pagination was called twice
	if callCount != 2 {
		t.Fatalf("expected 2 pagination calls, got %d", callCount)
	}
}

func Test_addObjectsToArchive_pagination(t *testing.T) {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	// Create a buffer to write tar archive
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)

	// Create a fake dynamic client with a scheme
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create clusters in different namespaces
	clusters := []*v1alpha1.Cluster{
		{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "Cluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster1",
				Namespace: "ns1",
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "Cluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster2",
				Namespace: "ns2",
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "fleet.cattle.io/v1alpha1",
				Kind:       "Cluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster3",
				Namespace: "ns3",
			},
		},
	}

	// Convert to unstructured
	var objs []*unstructured.Unstructured
	for _, cluster := range clusters {
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
		if err != nil {
			t.Fatalf("failed to convert to unstructured: %v", err)
		}
		objs = append(objs, &unstructured.Unstructured{Object: unstructuredObj})
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme)

	// Set up pagination: first page returns 2 clusters, second page returns 1
	callCount := 0
	fakeDynClient.PrependReactor("list", "clusters", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion("fleet.cattle.io/v1alpha1")
		list.SetKind("ClusterList")

		callCount++
		switch callCount {
		case 1:
			// First page: return first 2 clusters with continue token
			list.SetResourceVersion("1")
			list.SetContinue("continue-token")
			list.Items = []unstructured.Unstructured{*objs[0], *objs[1]}
		case 2:
			// Second page: return last cluster
			list.SetResourceVersion("2")
			list.SetContinue("")
			list.Items = []unstructured.Unstructured{*objs[2]}
		}

		return true, list, nil
	})

	// Test with fetchLimit = 2 (should trigger pagination)
	err := addObjectsToArchive(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "clusters", w, Options{FetchLimit: 2})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Close writers to flush data
	w.Close()
	gz.Close()

	// Read back the tar archive
	gzReader, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)

	// Should have three entries (one for each cluster)
	entries := make([]string, 0, 3)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar header: %v", err)
		}
		entries = append(entries, header.Name)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 tar entries, got %d: %v", len(entries), entries)
	}

	expectedEntries := []string{
		"clusters_ns1_cluster1",
		"clusters_ns2_cluster2",
		"clusters_ns3_cluster3",
	}
	if !slices.Equal(entries, expectedEntries) {
		t.Fatalf("expected tar entries %v, got %v", expectedEntries, entries)
	}

	// Verify pagination was called twice
	if callCount != 2 {
		t.Fatalf("expected 2 pagination calls, got %d", callCount)
	}
}

func Test_addMetrics(t *testing.T) {
	cases := []struct {
		name       string
		svcs       []corev1.Service
		svcListErr error
		pods       []corev1.Pod
		podListErr error
		expErrStr  string
		fetchLimit int64
	}{
		{
			name: "no services found",
		},
		{
			name:       "error fetching services",
			svcListErr: errors.New("something went wrong"),
			expErrStr:  "failed to list services for extracting metrics: something went wrong",
		},
		{
			name: "no monitoring services",
			svcs: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "not-monitoring-prefixed",
						Namespace: "cattle-fleet-system",
					},
				},
			},
			expErrStr: "",
		},
		{
			name: "monitoring service without exposed ports",
			svcs: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "monitoring-prefixed",
						Namespace: "cattle-fleet-system",
					},
				},
			},
			expErrStr: "service cattle-fleet-system/monitoring-prefixed does not have any exposed ports",
		},
		{
			name: "monitoring service with exposed ports but no labels",
			svcs: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "monitoring-prefixed",
						Namespace: "cattle-fleet-system",
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{
							{
								Port: 42,
							},
						},
					},
				},
			},
			expErrStr: "no app label found on service cattle-fleet-system/monitoring-prefixed",
		},
		{
			name: "monitoring service with exposed ports and label, but no pod",
			svcs: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "monitoring-prefixed",
						Namespace: "cattle-fleet-system",
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{
							{
								Port: 42,
							},
						},
						Selector: map[string]string{
							"app": "foo",
						},
					},
				},
			},
			expErrStr: "no pod found behind service cattle-fleet-system/monitoring-prefixed",
		},
		{
			name: "monitoring service with exposed ports and label, failure to get pod",
			svcs: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "monitoring-prefixed",
						Namespace: "cattle-fleet-system",
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{
							{
								Port: 42,
							},
						},
						Selector: map[string]string{
							"app": "foo",
						},
					},
				},
			},
			podListErr: errors.New("something went wrong"),
			expErrStr:  "failed to get pod behind service cattle-fleet-system/monitoring-prefixed: something went wrong",
		},
		{
			name: "monitoring service with exposed ports and label, more than one pod behind it",
			svcs: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "monitoring-prefixed",
						Namespace: "cattle-fleet-system",
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{
							{
								Port: 42,
							},
						},
						Selector: map[string]string{
							"app": "foo",
						},
					},
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod1",
						Namespace: "cattle-fleet-system",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod2",
						Namespace: "cattle-fleet-system",
					},
				},
			},
			expErrStr: "found more than one pod behind service cattle-fleet-system/monitoring-prefixed",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockClient := mocks.NewMockK8sClient(mockCtrl)
			ctx := context.Background()

			mockClient.EXPECT().List(
				ctx,
				gomock.AssignableToTypeOf(&corev1.ServiceList{}),
				client.InNamespace("cattle-fleet-system"),
				gomock.Any(), // client.Limit(...)
				gomock.Any(), // client.Continue(...)
			).
				DoAndReturn(
					func(_ context.Context, sl *corev1.ServiceList, _ ...client.ListOption) error {
						sl.Items = c.svcs

						return c.svcListErr
					},
				)

			// Possible call to get pods from the service if it is properly formed (port + label selector)
			mockClient.EXPECT().List(
				ctx,
				gomock.AssignableToTypeOf(&corev1.PodList{}),
				client.InNamespace("cattle-fleet-system"),
				gomock.Any(), // matching labels are added when limit handling is enabled
				gomock.Any(), // client.Limit(...)
				gomock.Any(), // client.Continue(...)
			). // pagination options are always appended
				DoAndReturn(
					func(_ context.Context, pl *corev1.PodList, _ ...client.ListOption) error {
						pl.Items = c.pods

						return c.podListErr
					},
				).
				AnyTimes()

			logger := log.FromContext(ctx).WithName("test-fleet-dump")

			err := addMetricsToArchive(ctx, mockClient, logger, nil, nil, Options{FetchLimit: c.fetchLimit}) // cfg and tar writer not needed for basic failure cases

			if (err == nil) != (c.expErrStr == "") {
				t.Fatalf("expected err %s, \n\tgot %s", c.expErrStr, err)
			}

			if err != nil && !strings.Contains(err.Error(), c.expErrStr) {
				t.Fatalf("expected error containing %q, got %q", c.expErrStr, err)
			}

		})
	}
}

func Test_addSecretsToArchive(t *testing.T) {
	cases := []struct {
		name         string
		secrets      []corev1.Secret
		secretErr    error
		metadataOnly bool
		expErrStr    string
	}{
		{
			name: "no secrets found",
		},
		{
			name:      "error fetching secrets",
			secretErr: errors.New("something went wrong"),
			expErrStr: "failed to list secrets for namespace",
		},
		{
			name: "secrets with full data",
			secrets: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"key": []byte("value"),
					},
				},
			},
			metadataOnly: false,
		},
		{
			name: "secrets with metadata only",
			secrets: []corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"key": []byte("value"),
					},
				},
			},
			metadataOnly: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockClient := mocks.NewMockK8sClient(mockCtrl)
			ctx := context.Background()

			scheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(scheme))
			utilruntime.Must(v1alpha1.AddToScheme(scheme))
			fakeDynClient := fake.NewSimpleDynamicClient(scheme)

			mockClient.EXPECT().List(
				ctx,
				gomock.AssignableToTypeOf(&corev1.SecretList{}),
				gomock.Any()).
				DoAndReturn(
					func(_ context.Context, sl *corev1.SecretList, _ ...client.ListOption) error {
						sl.Items = c.secrets
						return c.secretErr
					},
				).
				AnyTimes()

			logger := log.FromContext(ctx).WithName("test-fleet-dump")

			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)

			err := addSecretsToArchive(ctx, fakeDynClient, mockClient, logger, tw, c.metadataOnly, Options{FetchLimit: 0})

			if (err == nil) != (c.expErrStr == "") {
				t.Fatalf("expected err %s, \n\tgot %s", c.expErrStr, err)
			}

			if err != nil && !strings.Contains(err.Error(), c.expErrStr) {
				t.Fatalf("expected error containing %q, got %q", c.expErrStr, err)
			}

			// For metadata-only test, verify sensitive fields are stripped
			if c.metadataOnly && len(c.secrets) > 0 {
				tw.Close()
				tr := tar.NewReader(&buf)

				// Validate all secrets in the archive
				for i := 0; i < len(c.secrets); i++ {
					_, err := tr.Next()
					if err != nil {
						t.Fatalf("failed to read tar header for secret %d: %v", i, err)
					}
					data, err := io.ReadAll(tr)
					if err != nil {
						t.Fatalf("failed to read tar content for secret %d: %v", i, err)
					}

					var secret corev1.Secret
					if err := yaml.Unmarshal(data, &secret); err != nil {
						t.Fatalf("failed to unmarshal secret %d: %v", i, err)
					}

					if secret.Data != nil {
						t.Errorf("expected Data field to be nil in metadata-only mode for secret %d, got %v", i, secret.Data)
					}

					// Verify metadata is preserved
					if secret.Name != c.secrets[i].Name {
						t.Errorf("expected Name %q to be preserved in metadata-only mode for secret %d, got %v", c.secrets[i].Name, i, secret.Name)
					}
					if secret.Namespace != c.secrets[i].Namespace {
						t.Errorf("expected Namespace %q to be preserved in metadata-only mode for secret %d, got %v", c.secrets[i].Namespace, i, secret.Namespace)
					}
				}
			}
		})
	}
}

func Test_addContentsToArchive(t *testing.T) {
	cases := []struct {
		name         string
		contents     []runtime.Object
		metadataOnly bool
		expErrStr    string
	}{
		{
			name: "no contents found",
		},
		{
			name: "contents with full data",
			contents: []runtime.Object{
				&v1alpha1.Content{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-content",
						Namespace: "default",
					},
					Content:   []byte("test-content-data"),
					SHA256Sum: "abc123def456",
				},
			},
			metadataOnly: false,
		},
		{
			name: "contents with metadata only",
			contents: []runtime.Object{
				&v1alpha1.Content{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-content",
						Namespace: "default",
					},
					Content:   []byte("test-content-data"),
					SHA256Sum: "abc123def456",
				},
			},
			metadataOnly: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()

			scheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(scheme))
			utilruntime.Must(v1alpha1.AddToScheme(scheme))
			fakeDynClient := fake.NewSimpleDynamicClient(scheme, c.contents...)

			logger := log.FromContext(ctx).WithName("test-fleet-dump")

			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)

			err := addContentsToArchive(ctx, fakeDynClient, logger, tw, c.metadataOnly, nil, Options{FetchLimit: 0})

			if (err == nil) != (c.expErrStr == "") {
				t.Fatalf("expected err %s, \n\tgot %s", c.expErrStr, err)
			}

			if err != nil && !strings.Contains(err.Error(), c.expErrStr) {
				t.Fatalf("expected error containing %q, got %q", c.expErrStr, err)
			}

			// For metadata-only test, verify sensitive fields are stripped
			if c.metadataOnly && len(c.contents) > 0 {
				tw.Close()
				tr := tar.NewReader(&buf)
				header, err := tr.Next()
				if err != nil {
					t.Fatalf("failed to read tar header: %v", err)
				}
				data, err := io.ReadAll(tr)
				if err != nil {
					t.Fatalf("failed to read tar content: %v", err)
				}

				var content v1alpha1.Content
				if err := yaml.Unmarshal(data, &content); err != nil {
					t.Fatalf("failed to unmarshal content: %v", err)
				}

				if content.Content != nil {
					t.Errorf("expected content field to be nil in metadata-only mode, got %v", content.Content)
				}

				if content.SHA256Sum != "abc123def456" {
					t.Errorf("expected sha256sum to be preserved in metadata-only mode, got %v", content.SHA256Sum)
				}

				t.Logf("header: %v", header)
			}
		})
	}
}

func Test_collectBundleNamesByGitRepo(t *testing.T) {
	tests := []struct {
		name          string
		bundles       []runtime.Object
		namespace     string
		gitrepo       string
		fetchLimit    int64
		expectedNames []string
		wantErr       bool
	}{
		{
			name:          "no bundles found",
			bundles:       []runtime.Object{},
			namespace:     "fleet-local",
			gitrepo:       "my-repo",
			expectedNames: []string{},
			wantErr:       false,
		},
		{
			name: "filter bundles by gitrepo label",
			bundles: []runtime.Object{
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle1",
						Namespace: "fleet-local",
						Labels: map[string]string{
							"fleet.cattle.io/repo-name": "my-repo",
						},
					},
				},
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle2",
						Namespace: "fleet-local",
						Labels: map[string]string{
							"fleet.cattle.io/repo-name": "other-repo",
						},
					},
				},
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle3",
						Namespace: "fleet-local",
						Labels: map[string]string{
							"fleet.cattle.io/repo-name": "my-repo",
						},
					},
				},
			},
			namespace:     "fleet-local",
			gitrepo:       "my-repo",
			expectedNames: []string{"bundle1", "bundle3"},
			wantErr:       false,
		},
		{
			name: "pagination with gitrepo filter",
			bundles: []runtime.Object{
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle1",
						Namespace: "fleet-local",
						Labels: map[string]string{
							"fleet.cattle.io/repo-name": "my-repo",
						},
					},
				},
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle2",
						Namespace: "fleet-local",
						Labels: map[string]string{
							"fleet.cattle.io/repo-name": "my-repo",
						},
					},
				},
			},
			namespace:     "fleet-local",
			gitrepo:       "my-repo",
			fetchLimit:    1, // Force pagination
			expectedNames: []string{"bundle1", "bundle2"},
			wantErr:       false,
		},
		{
			name: "bundles without label are excluded",
			bundles: []runtime.Object{
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle1",
						Namespace: "fleet-local",
						Labels: map[string]string{
							"fleet.cattle.io/repo-name": "my-repo",
						},
					},
				},
				&v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bundle2",
						Namespace: "fleet-local",
						// No labels
					},
				},
			},
			namespace:     "fleet-local",
			gitrepo:       "my-repo",
			expectedNames: []string{"bundle1"},
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(scheme))
			utilruntime.Must(v1alpha1.AddToScheme(scheme))

			fakeDynClient := fake.NewSimpleDynamicClient(scheme, tt.bundles...)
			ctx := context.Background()

			names, err := collectBundleNamesByGitRepo(ctx, fakeDynClient, tt.namespace, tt.gitrepo, tt.fetchLimit)

			if (err != nil) != tt.wantErr {
				t.Fatalf("collectBundleNamesByGitRepo() error = %v, wantErr %v", err, tt.wantErr)
			}

			if len(names) != len(tt.expectedNames) {
				t.Fatalf("expected %d bundle names, got %d: %v", len(tt.expectedNames), len(names), names)
			}

			for i, name := range names {
				if name != tt.expectedNames[i] {
					t.Errorf("expected bundle name %q at index %d, got %q", tt.expectedNames[i], i, name)
				}
			}
		})
	}
}

func Test_GitRepoFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create test data: 2 GitRepos with their bundles and bundledeployments
	objs := []runtime.Object{
		// GitRepo 1: my-repo
		&v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-repo",
				Namespace: "fleet-local",
			},
		},
		// GitRepo 2: other-repo
		&v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-repo",
				Namespace: "fleet-local",
			},
		},
		// Bundles from my-repo
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bundle-1",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/repo-name": "my-repo",
				},
			},
		},
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bundle-2",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/repo-name": "my-repo",
				},
			},
		},
		// Bundle from other-repo
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bundle",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/repo-name": "other-repo",
				},
			},
		},
		// BundleDeployments from my-repo bundles
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bd-1",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "my-bundle-1",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bd-2",
				Namespace: "cluster-ns-2",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "my-bundle-2",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		// BundleDeployment from other-repo
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bd",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "other-bundle",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)

	// Test filtering by GitRepo
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	opt := Options{
		FetchLimit: 0,
		Namespace:  "fleet-local",
		GitRepo:    "my-repo",
	}

	// Collect bundle names for my-repo
	bundleNames, err := collectBundleNamesByGitRepo(ctx, fakeDynClient, opt.Namespace, opt.GitRepo, opt.FetchLimit)
	if err != nil {
		t.Fatalf("failed to collect bundle names: %v", err)
	}

	if len(bundleNames) != 2 {
		t.Fatalf("expected 2 bundles for my-repo, got %d", len(bundleNames))
	}

	// Test addObjectsWithNameFilter for GitRepos
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "gitrepos", tw, []string{"my-repo"}, opt)
	if err != nil {
		t.Fatalf("failed to add gitrepos: %v", err)
	}

	// Test addObjectsWithNameFilter for Bundles
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "bundles", tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundles: %v", err)
	}

	// Test addBundleDeployments with bundle name filter
	err = addBundleDeployments(ctx, fakeDynClient, logger, tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundledeployments: %v", err)
	}

	tw.Close()
	gz.Close()

	// Read back the tar archive and verify contents
	gzReader, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)

	entries := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar header: %v", err)
		}

		// Read the content to verify it's valid YAML
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("failed to read tar content: %v", err)
		}

		// Verify it can be unmarshaled
		var obj map[string]interface{}
		if err := yaml.Unmarshal(data, &obj); err != nil {
			t.Fatalf("failed to unmarshal %s: %v", header.Name, err)
		}

		entries[header.Name] = true
	}

	// Verify expected entries are present
	expectedEntries := []string{
		"gitrepos_fleet-local_my-repo",
		"bundles_fleet-local_my-bundle-1",
		"bundles_fleet-local_my-bundle-2",
		"bundledeployments_cluster-ns-1_my-bd-1",
		"bundledeployments_cluster-ns-2_my-bd-2",
	}

	for _, expected := range expectedEntries {
		if !entries[expected] {
			t.Errorf("expected entry %q not found in archive", expected)
		}
	}

	// Verify excluded entries are not present
	excludedEntries := []string{
		"gitrepos_fleet-local_other-repo",
		"bundles_fleet-local_other-bundle",
		"bundledeployments_cluster-ns-1_other-bd",
	}

	for _, excluded := range excludedEntries {
		if entries[excluded] {
			t.Errorf("excluded entry %q should not be in archive", excluded)
		}
	}

	t.Logf("Archive contains %d entries (expected 5)", len(entries))
}

func Test_buildBundleNameSelector(t *testing.T) {
	tests := []struct {
		name        string
		namespace   string
		bundleNames []string
		expected    string
	}{
		{
			name:        "no bundle names",
			namespace:   "fleet-local",
			bundleNames: []string{},
			expected:    "fleet.cattle.io/bundle-namespace=fleet-local",
		},
		{
			name:        "single bundle name",
			namespace:   "fleet-local",
			bundleNames: []string{"bundle1"},
			expected:    "fleet.cattle.io/bundle-namespace=fleet-local,fleet.cattle.io/bundle-name in (bundle1)",
		},
		{
			name:        "multiple bundle names",
			namespace:   "fleet-local",
			bundleNames: []string{"bundle1", "bundle2", "bundle3"},
			expected:    "fleet.cattle.io/bundle-namespace=fleet-local,fleet.cattle.io/bundle-name in (bundle1,bundle2,bundle3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildBundleNameSelector(tt.namespace, tt.bundleNames)
			if result != tt.expected {
				t.Errorf("buildBundleNameSelector() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func Test_BundleFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create test data: 3 Bundles with their bundledeployments
	objs := []runtime.Object{
		// Bundle 1: target-bundle (the one we'll filter by)
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "target-bundle",
				Namespace: "fleet-local",
			},
		},
		// Bundle 2: other-bundle-1
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bundle-1",
				Namespace: "fleet-local",
			},
		},
		// Bundle 3: other-bundle-2
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bundle-2",
				Namespace: "fleet-local",
			},
		},
		// BundleDeployments from target-bundle
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "target-bd-1",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "target-bundle",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "target-bd-2",
				Namespace: "cluster-ns-2",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "target-bundle",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		// BundleDeployments from other bundles
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bd-1",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "other-bundle-1",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bd-2",
				Namespace: "cluster-ns-2",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "other-bundle-2",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)

	// Test filtering by Bundle
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	opt := Options{
		FetchLimit: 0,
		Namespace:  "fleet-local",
		Bundle:     "target-bundle",
	}

	// Verify bundle exists
	exists, err := bundleExists(ctx, fakeDynClient, opt.Namespace, opt.Bundle)
	if err != nil {
		t.Fatalf("failed to check if bundle exists: %v", err)
	}
	if !exists {
		t.Fatalf("bundle %q should exist", opt.Bundle)
	}

	// Bundle filter: bundleNames is set directly
	bundleNames := []string{opt.Bundle}

	// Test addObjectsWithNameFilter for Bundles
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "bundles", tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundles: %v", err)
	}

	// Test addBundleDeployments with bundle name filter
	err = addBundleDeployments(ctx, fakeDynClient, logger, tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundledeployments: %v", err)
	}

	tw.Close()
	gz.Close()

	// Read back the tar archive and verify contents
	gzReader, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)

	entries := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar header: %v", err)
		}

		// Read the content to verify it's valid YAML
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("failed to read tar content: %v", err)
		}

		// Verify it can be unmarshaled
		var obj map[string]interface{}
		if err := yaml.Unmarshal(data, &obj); err != nil {
			t.Fatalf("failed to unmarshal %s: %v", header.Name, err)
		}

		entries[header.Name] = true
	}

	// Verify expected entries are present
	expectedEntries := []string{
		"bundles_fleet-local_target-bundle",
		"bundledeployments_cluster-ns-1_target-bd-1",
		"bundledeployments_cluster-ns-2_target-bd-2",
	}

	for _, expected := range expectedEntries {
		if !entries[expected] {
			t.Errorf("expected entry %q not found in archive", expected)
		}
	}

	// Verify excluded entries are not present
	excludedEntries := []string{
		"bundles_fleet-local_other-bundle-1",
		"bundles_fleet-local_other-bundle-2",
		"bundledeployments_cluster-ns-1_other-bd-1",
		"bundledeployments_cluster-ns-2_other-bd-2",
	}

	for _, excluded := range excludedEntries {
		if entries[excluded] {
			t.Errorf("excluded entry %q should not be in archive", excluded)
		}
	}

	t.Logf("Archive contains %d entries (expected 3)", len(entries))
}

func Test_bundleExists(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create test data with one bundle
	objs := []runtime.Object{
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "existing-bundle",
				Namespace: "fleet-local",
			},
		},
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)

	tests := []struct {
		name       string
		namespace  string
		bundleName string
		wantExists bool
		wantErr    bool
	}{
		{
			name:       "bundle exists",
			namespace:  "fleet-local",
			bundleName: "existing-bundle",
			wantExists: true,
			wantErr:    false,
		},
		{
			name:       "bundle does not exist",
			namespace:  "fleet-local",
			bundleName: "nonexistent-bundle",
			wantExists: false,
			wantErr:    false,
		},
		{
			name:       "bundle in different namespace",
			namespace:  "other-namespace",
			bundleName: "existing-bundle",
			wantExists: false,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := bundleExists(ctx, fakeDynClient, tt.namespace, tt.bundleName)
			if (err != nil) != tt.wantErr {
				t.Errorf("bundleExists() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if exists != tt.wantExists {
				t.Errorf("bundleExists() = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func Test_HelmOpFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create test data: 2 HelmOps with their bundles and bundledeployments
	objs := []runtime.Object{
		// HelmOp 1: my-helmop
		&v1alpha1.HelmOp{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-helmop",
				Namespace: "fleet-local",
			},
		},
		// HelmOp 2: other-helmop
		&v1alpha1.HelmOp{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-helmop",
				Namespace: "fleet-local",
			},
		},
		// Bundles from my-helmop
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bundle-1",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/fleet-helm-name": "my-helmop",
				},
			},
		},
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bundle-2",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/fleet-helm-name": "my-helmop",
				},
			},
		},
		// Bundle from other-helmop
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bundle",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/fleet-helm-name": "other-helmop",
				},
			},
		},
		// BundleDeployments from my-helmop bundles
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bd-1",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "my-bundle-1",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bd-2",
				Namespace: "cluster-ns-2",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "my-bundle-2",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		// BundleDeployment from other-helmop
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bd",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "other-bundle",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)

	// Test filtering by HelmOp
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	opt := Options{
		FetchLimit: 0,
		Namespace:  "fleet-local",
		HelmOp:     "my-helmop",
	}

	// Collect bundle names for my-helmop
	bundleNames, err := collectBundleNamesByHelmOp(ctx, fakeDynClient, opt.Namespace, opt.HelmOp, opt.FetchLimit)
	if err != nil {
		t.Fatalf("failed to collect bundle names: %v", err)
	}

	if len(bundleNames) != 2 {
		t.Fatalf("expected 2 bundles for my-helmop, got %d", len(bundleNames))
	}

	// Test addObjectsWithNameFilter for HelmOps
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "helmops", tw, []string{"my-helmop"}, opt)
	if err != nil {
		t.Fatalf("failed to add helmops: %v", err)
	}

	// Test addObjectsWithNameFilter for Bundles
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "bundles", tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundles: %v", err)
	}

	// Test addBundleDeployments with bundle name filter
	err = addBundleDeployments(ctx, fakeDynClient, logger, tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundledeployments: %v", err)
	}

	tw.Close()
	gz.Close()

	// Read back the tar archive and verify contents
	gzReader, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)

	entries := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar header: %v", err)
		}

		// Read and validate YAML content
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("failed to read tar content: %v", err)
		}

		var obj map[string]interface{}
		if err := yaml.Unmarshal(data, &obj); err != nil {
			t.Fatalf("failed to unmarshal %s: %v", header.Name, err)
		}

		entries[header.Name] = true
	}

	// Verify expected entries are present
	expectedEntries := []string{
		"helmops_fleet-local_my-helmop",
		"bundles_fleet-local_my-bundle-1",
		"bundles_fleet-local_my-bundle-2",
		"bundledeployments_cluster-ns-1_my-bd-1",
		"bundledeployments_cluster-ns-2_my-bd-2",
	}

	for _, expected := range expectedEntries {
		if !entries[expected] {
			t.Errorf("expected entry %q not found in archive", expected)
		}
	}

	// Verify excluded entries are not present
	excludedEntries := []string{
		"helmops_fleet-local_other-helmop",
		"bundles_fleet-local_other-bundle",
		"bundledeployments_cluster-ns-1_other-bd",
	}

	for _, excluded := range excludedEntries {
		if entries[excluded] {
			t.Errorf("excluded entry %q should not be in archive", excluded)
		}
	}

	t.Logf("Archive contains %d entries (expected 5)", len(entries))
}
