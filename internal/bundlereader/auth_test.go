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
		{
			name: "insecureSkipVerify is set to true",
			secretData: map[string][]byte{
				"insecureSkipVerify": []byte("true"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				InsecureSkipVerify: true,
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "insecureSkipVerify is set to an invalid value",
			secretData: map[string][]byte{
				"insecureSkipVerify": []byte("THIS_IS_NOT_A_VALID_VALUE"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				InsecureSkipVerify: false,
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "basicHTTP is set to true",
			secretData: map[string][]byte{
				"basicHTTP": []byte("true"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				BasicHTTP: true,
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
		{
			name: "basicHTTP is set to an invalid value",
			secretData: map[string][]byte{
				"basicHTTP": []byte("THIS_IS_NOT_A_VALID_VALUE"),
			},
			getError: "",
			expectedAuth: bundlereader.Auth{
				BasicHTTP: false,
			},
			expectedErrNotNil: false,
			expectedError:     "",
		},
	}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	mockClient := mocks.NewMockK8sClient(mockCtrl)

	assert := assert.New(t)
	for _, c := range cases {
		if c.getError != "" {
			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
					return fmt.Errorf("%v", c.getError)
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

func TestAuth_Hash(t *testing.T) {
	for name, baseAuth := range map[string]bundlereader.Auth{
		"no fields": {},
		"all fields": {
			Username:           "user",
			Password:           "pass",
			CABundle:           []byte("ca-data"),
			SSHPrivateKey:      []byte("ssh-key"),
			InsecureSkipVerify: true,
			BasicHTTP:          false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Test that changing each field individually results in a new hash.
			testCases := []struct {
				name          string
				mod           func(a bundlereader.Auth) bundlereader.Auth
				auth          bundlereader.Auth
				baseHash      string
				expectedEqual bool
			}{
				{
					name:          "No changes",
					mod:           func(a bundlereader.Auth) bundlereader.Auth { return a },
					expectedEqual: true,
				},
				{
					name: "Different Username",
					mod:  func(a bundlereader.Auth) bundlereader.Auth { a.Username = "different-user"; return a },
				},
				{
					name: "Different Password",
					mod:  func(a bundlereader.Auth) bundlereader.Auth { a.Password = "different-pass"; return a },
				},
				{
					name: "Different CABundle",
					mod:  func(a bundlereader.Auth) bundlereader.Auth { a.CABundle = []byte("different-ca"); return a },
				},
				{
					name: "Different SSHPrivateKey",
					mod:  func(a bundlereader.Auth) bundlereader.Auth { a.SSHPrivateKey = []byte("different-key"); return a },
				},
				{
					name: "Different InsecureSkipVerify",
					mod:  func(a bundlereader.Auth) bundlereader.Auth { a.InsecureSkipVerify = !a.InsecureSkipVerify; return a },
				},
				{
					name: "Different BasicHTTP",
					mod:  func(a bundlereader.Auth) bundlereader.Auth { a.BasicHTTP = !a.BasicHTTP; return a },
				},
			}

			for _, tc := range testCases {
				t.Run(tc.name, func(t *testing.T) {
					baseHash := baseAuth.Hash()
					modifiedAuth := tc.mod(baseAuth)
					modifiedHash := modifiedAuth.Hash()

					if tc.expectedEqual {
						assert.Equal(t, modifiedHash, baseHash)
					} else {
						assert.NotEqual(t, modifiedHash, baseHash)
					}
				})
			}
		})
	}
}
