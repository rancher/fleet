package cert_test

import (
	"context"
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/cert"
)

func TestGetRancherCABundle(t *testing.T) {
	notFoundErr := &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
	testCases := []struct {
		name           string
		secretGets     func(c *mocks.MockK8sClient)
		expectedBundle []byte
		expectedErr    error
	}{
		{
			name: "no secrets found",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						// nothing done: object stays nil
						return notFoundErr
					}).Times(2)
			},
			expectedBundle: nil,
			expectedErr:    nil,
		},
		{
			name: "tls-ca exists but tls-ca-additional does not",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"cacerts.pem": []byte("foo")}
						return nil
					})

				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca-additional"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						return notFoundErr
					})
			},
			expectedBundle: []byte("foo"),
			expectedErr:    nil,
		},
		{
			name: "tls-ca does not exist but tls-ca-additional does",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						return notFoundErr
					})

				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca-additional"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"ca-additional.pem": []byte("foo")}
						return nil
					})
			},
			expectedBundle: []byte("foo"),
			expectedErr:    nil,
		},
		{
			name: "tls-ca and tls-ca-additional both exist",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"cacerts.pem": []byte("foo")}
						return nil
					})

				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca-additional"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"ca-additional.pem": []byte("bar")}
						return nil
					})
			},
			expectedBundle: []byte("foobar"),
			expectedErr:    nil,
		},
		{
			name: "tls-ca is malformed",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"certs.pem": []byte("foo")}
						return nil
					})
			},
			expectedBundle: nil,
			expectedErr:    fmt.Errorf("no field cacerts.pem found in secret tls-ca"),
		},
		{
			name: "tls-ca and tls-ca-additional both exist, but tls-ca-additional is malformed",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"cacerts.pem": []byte("foo")}
						return nil
					})

				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca-additional"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"moar-ca.pem": []byte("bar")}
						return nil
					})
			},
			expectedBundle: nil,
			expectedErr:    fmt.Errorf("no field ca-additional.pem found in secret tls-ca-additional"),
		},
		{
			name: "client fails to get tls-ca secret",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						return fmt.Errorf("something happened")
					})
			},
			expectedBundle: nil,
			expectedErr:    fmt.Errorf("something happened"),
		},
		{
			name: "client fails to get tls-ca-additional secret",
			secretGets: func(c *mocks.MockK8sClient) {
				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						secret.Data = map[string][]byte{"cacerts.pem": []byte("foo")}
						return nil
					})

				c.EXPECT().Get(
					gomock.Any(),
					types.NamespacedName{Namespace: "cattle-system", Name: "tls-ca-additional"},
					gomock.Any(),
				).DoAndReturn(
					func(ctx context.Context, key client.ObjectKey, secret *corev1.Secret, opts ...client.GetOption) error {
						return fmt.Errorf("something happened")
					})
			},
			expectedBundle: nil,
			expectedErr:    fmt.Errorf("something happened"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			ctlr := gomock.NewController(t)
			mockClient := mocks.NewMockK8sClient(ctlr)

			tc.secretGets(mockClient)

			bundle, err := cert.GetRancherCABundle(context.Background(), mockClient)
			switch {
			case tc.expectedErr == nil && err != nil:
				t.Errorf("expected nil error, got %q", err.Error())
			case tc.expectedErr != nil && err == nil:
				t.Errorf("expected error %q, got nil", tc.expectedErr.Error())
			case err != nil && tc.expectedErr != nil && err.Error() != tc.expectedErr.Error():
				t.Errorf("expected %q, got %q", tc.expectedErr.Error(), err.Error())
			}

			if string(bundle) != string(tc.expectedBundle) { // XXX: should we compare byte by byte instead?
				t.Errorf("expected %q, got %q", tc.expectedBundle, bundle)
			}
		})
	}
}
