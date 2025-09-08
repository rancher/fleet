package ssh_test

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/internal/ssh"
)

func TestGetKnownHosts(t *testing.T) {
	tests := map[string]struct {
		isStrict       bool
		secret         *corev1.Secret
		fallbackSecret *corev1.Secret
		configMap      *corev1.ConfigMap
		ns             string
		secretName     string
		expectedData   string
		expectedErr    string
	}{
		"no secret, no config map": {
			ns:           "foo",
			secretName:   "bar",
			expectedData: "",
			expectedErr:  "deployment is incomplete",
		},
		"secret exists but does not contain known_hosts data, no config map": {
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "",
			expectedErr:  "deployment is incomplete",
		},
		"secret exists with known_hosts data, no config map": {
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
				Data: map[string][]byte{
					"known_hosts": []byte("somedata"),
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "somedata",
		},
		"secret exists without known_hosts data, but config map exists": {
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "somedata",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "somedata",
		},
		"secret does not exist, but config map exists": {
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "somedata",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "somedata",
		},
		"both secret and config map exist": { // secret has precedence
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
				Data: map[string][]byte{
					"known_hosts": []byte("somedata_from_secret"),
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "somedata_from_configmap",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "somedata_from_secret",
		},
		"secret, fallback secret and config map exist": { // secret still has precedence
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
				Data: map[string][]byte{
					"known_hosts": []byte("somedata_from_secret"),
				},
			},
			fallbackSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "gitcredential",
				},
				Data: map[string][]byte{
					"known_hosts": []byte("somedata_from_gitcredential"),
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "somedata_from_configmap",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "somedata_from_secret",
		},
		"empty namespace": {
			ns:           "",
			secretName:   "bar",
			expectedData: "",
			expectedErr:  "empty namespace",
		},
		"empty secret name, no config map": {
			ns:           "foo",
			secretName:   "",
			expectedData: "",
			expectedErr:  "deployment is incomplete",
		},
		"empty secret name, but gitcredential secret exists": {
			fallbackSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "gitcredential",
				},
				Data: map[string][]byte{
					"known_hosts": []byte("somedata"),
				},
			},
			ns:           "foo",
			secretName:   "",
			expectedData: "somedata",
		},
		"empty secret name, but both gitcredential secret and config map exist": {
			fallbackSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "gitcredential",
				},
				Data: map[string][]byte{
					"known_hosts": []byte("somedata_gitcredential"),
				},
			},
			ns: "foo",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "somedata_configmap",
				},
			},
			secretName:   "",
			expectedData: "somedata_gitcredential",
		},
		"empty secret name, but config map exists": {
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "somedata",
				},
			},
			ns:           "foo",
			secretName:   "",
			expectedData: "somedata",
		},
		"empty data, without enforcement": {
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
				Data: map[string][]byte{
					"known_hosts": []byte(""),
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "",
			expectedErr:  "",
		},
		"empty data, with enforcement": {
			isStrict: true,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "bar",
				},
				Data: map[string][]byte{
					"known_hosts": []byte(""),
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "cattle-fleet-system",
					Name:      "known-hosts",
				},
				Data: map[string]string{
					"known_hosts": "",
				},
			},
			ns:           "foo",
			secretName:   "bar",
			expectedData: "",
			expectedErr:  "strict host key checks are enforced",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			client := mocks.NewMockK8sClient(mockCtrl)

			nsn := types.NamespacedName{
				Namespace: test.ns,
				Name:      test.secretName,
			}

			client.EXPECT().Get(gomock.Any(), nsn, secretPointerMatcher{}, gomock.Any()).MaxTimes(1).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, secret *corev1.Secret, opts ...interface{}) error {
					if test.secret == nil {
						return apierrors.NewNotFound(schema.GroupResource{}, "TEST ERROR")
					}

					secret.ObjectMeta = test.secret.ObjectMeta
					secret.Data = test.secret.Data

					return nil
				},
			)

			nsn.Name = "gitcredential"

			client.EXPECT().Get(gomock.Any(), nsn, secretPointerMatcher{}, gomock.Any()).MaxTimes(1).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, secret *corev1.Secret, opts ...interface{}) error {
					if test.fallbackSecret == nil {
						return apierrors.NewNotFound(schema.GroupResource{}, "TEST ERROR")
					}

					secret.ObjectMeta = test.fallbackSecret.ObjectMeta
					secret.Data = test.fallbackSecret.Data

					return nil
				},
			)

			client.EXPECT().Get(gomock.Any(), gomock.Any(), configMapPointerMatcher{}, gomock.Any()).MaxTimes(1).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, c *corev1.ConfigMap, opts ...interface{}) error {
					if test.configMap == nil {
						return apierrors.NewNotFound(schema.GroupResource{}, "TEST ERROR")
					}

					c.ObjectMeta = test.configMap.ObjectMeta
					c.Data = test.configMap.Data

					return nil
				},
			)

			getter := ssh.KnownHosts{EnforceHostKeyChecks: test.isStrict}
			data, err := getter.Get(context.TODO(), client, test.ns, test.secretName)

			if (err == nil && test.expectedErr != "") || (err != nil && test.expectedErr == "") {
				t.Errorf("expected error to match %q, got %v", test.expectedErr, err)
			}
			if err != nil && !strings.Contains(err.Error(), test.expectedErr) {
				t.Errorf("expected error to match %q, got %v", test.expectedErr, err)
			}
			if data != test.expectedData {
				t.Errorf("expected data %q, got %q", test.expectedData, data)
			}
		})
	}
}

type configMapPointerMatcher struct{}

func (m configMapPointerMatcher) Matches(x interface{}) bool {
	_, ok := x.(*corev1.ConfigMap)
	return ok
}

func (m configMapPointerMatcher) String() string {
	return ""
}

type secretPointerMatcher struct{}

func (m secretPointerMatcher) Matches(x interface{}) bool {
	_, ok := x.(*corev1.Secret)
	return ok
}

func (m secretPointerMatcher) String() string {
	return ""
}
