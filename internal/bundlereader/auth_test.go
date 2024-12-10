package bundlereader_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/mocks"
)

// nolint: funlen
func TestReadHelmAuthFromSecret(t *testing.T) {
	cases := []struct {
		name              string
		secretData        map[string][]byte
		getError          string
		expectedAuth      bundlereader.Auth
		expectedErrNotNil bool
		expectedError     string
	}{
		{
			name:         "nothing is set",
			secretData:   map[string][]byte{},
			getError:     "",
			expectedAuth: bundlereader.Auth{
				// default values
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "username, password and caBundle are set",
			secretData: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("user"),
				corev1.BasicAuthPasswordKey: []byte("passwd"),
				"cacerts":                   []byte("test_cabundle"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				Username: "user",
				Password: "passwd",
				CABundle: []byte("test_cabundle"),
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "username, password are set, caBundle is not",
			secretData: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("user"),
				corev1.BasicAuthPasswordKey: []byte("passwd"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				Username: "user",
				Password: "passwd",
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "caBundle is set, username and password are not",
			secretData: map[string][]byte{
				"cacerts": []byte("test_cabundle"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				CABundle: []byte("test_cabundle"),
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "username, caBundle are set, password is not",
			secretData: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("user"),
				"cacerts":                   []byte("test_cabundle"),
			},
			getError:          "",
			expectedAuth:      bundlereader.Auth{},
			expectedErrNotNil: true,
			expectedError:     "username is set in the secret, but password isn't",
		},
		{
			name: "username is set, password and caBundle are not",
			secretData: map[string][]byte{
				corev1.BasicAuthUsernameKey: []byte("user"),
			},
			getError:          "",
			expectedAuth:      bundlereader.Auth{},
			expectedErrNotNil: true,
			expectedError:     "username is set in the secret, but password isn't",
		},
		{
			name: "password, caBundle are set, username is not",
			secretData: map[string][]byte{
				corev1.BasicAuthPasswordKey: []byte("passwd"),
				"cacerts":                   []byte("test_cabundle"),
			},
			getError:          "",
			expectedAuth:      bundlereader.Auth{},
			expectedErrNotNil: true,
			expectedError:     "password is set in the secret, but username isn't",
		},
		{
			name: "password is set, username and caBundle are not",
			secretData: map[string][]byte{
				corev1.BasicAuthPasswordKey: []byte("passwd"),
			},
			getError:          "",
			expectedAuth:      bundlereader.Auth{},
			expectedErrNotNil: true,
			expectedError:     "password is set in the secret, but username isn't",
		},
		{
			name: "username, password and caBundle are set, but we get an error getting the secret",
			secretData: map[string][]byte{
				corev1.BasicAuthPasswordKey: []byte("passwd"),
			},
			getError:          "error getting secret",
			expectedAuth:      bundlereader.Auth{},
			expectedErrNotNil: true,
			expectedError:     "error getting secret",
		},
	}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockClient := mocks.NewMockClient(mockCtrl)

	assert := assert.New(t)
	for _, c := range cases {
		if c.getError != "" {
			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
					return fmt.Errorf(c.getError) // nolint:govet
				},
			)
		} else {
			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
					secret.Data = c.secretData
					return nil
				},
			)
		}

		nsName := types.NamespacedName{Name: "test", Namespace: "test"}
		auth, err := bundlereader.ReadHelmAuthFromSecret(context.TODO(), mockClient, nsName)
		assert.Equal(c.expectedErrNotNil, err != nil)
		if err != nil && c.expectedErrNotNil {
			assert.Equal(c.expectedError, err.Error())
		}
		assert.Equal(auth, c.expectedAuth)
	}
}
