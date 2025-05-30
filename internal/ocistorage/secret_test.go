package ocistorage

import (
	"context"
	"errors"
	"fmt"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCIOpts loaded from secret", func() {
	var (
		ctrl                   *gomock.Controller
		secretGetErrorMessage  string
		secretGetNotFoundError bool
		mockClient             *mocks.MockClient

		secretName string
		secretData map[string][]byte
		secretType string
	)

	JustBeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = mocks.NewMockClient(ctrl)
		ns := types.NamespacedName{Name: secretName, Namespace: "test"}
		getSecretFromMockClient(
			mockClient,
			ns,
			secretData,
			secretType,
			secretGetNotFoundError,
			secretGetErrorMessage,
		)
	})

	When("the given oci storage secret exists with all fields set", func() {
		BeforeEach(func() {
			secretName = "test"
			secretData = map[string][]byte{
				OCISecretUsername:      []byte("username"),
				OCISecretPassword:      []byte("password"),
				OCISecretAgentUsername: []byte("agentUsername"),
				OCISecretAgentPassword: []byte("agentPassword"),
				OCISecretReference:     []byte("reference"),
				OCISecretBasicHTTP:     []byte("true"),
				OCISecretInsecure:      []byte("true"),
			}
			secretType = fleet.SecretTypeOCIStorage
			secretGetErrorMessage = ""
			secretGetNotFoundError = false
		})
		It("returns the expected OCIOpts from the data in the secret", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			opts, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).ToNot(HaveOccurred())
			Expect(opts.Reference).To(Equal(string(secretData[OCISecretReference])))
			Expect(opts.Username).To(Equal(string(secretData[OCISecretUsername])))
			Expect(opts.Password).To(Equal(string(secretData[OCISecretPassword])))
			Expect(opts.AgentUsername).To(Equal(string(secretData[OCISecretAgentUsername])))
			Expect(opts.AgentPassword).To(Equal(string(secretData[OCISecretAgentPassword])))
			Expect(opts.BasicHTTP).To(BeTrue())
			Expect(opts.InsecureSkipTLS).To(BeTrue())
		})
	})

	When("the secret name is not set, but a default secret exists", func() {
		BeforeEach(func() {
			secretName = ""
			secretData = map[string][]byte{
				OCISecretUsername:      []byte("username"),
				OCISecretPassword:      []byte("password"),
				OCISecretAgentUsername: []byte("agentUsername"),
				OCISecretAgentPassword: []byte("agentPassword"),
				OCISecretReference:     []byte("reference"),
				OCISecretBasicHTTP:     []byte("true"),
				OCISecretInsecure:      []byte("true"),
			}
			secretType = fleet.SecretTypeOCIStorage
			secretGetErrorMessage = ""
			secretGetNotFoundError = false
		})
		It("returns the expected OCIOpts from the data in the secret", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			opts, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).ToNot(HaveOccurred())
			Expect(opts.Reference).To(Equal(string(secretData[OCISecretReference])))
			Expect(opts.Username).To(Equal(string(secretData[OCISecretUsername])))
			Expect(opts.Password).To(Equal(string(secretData[OCISecretPassword])))
			Expect(opts.AgentUsername).To(Equal(string(secretData[OCISecretAgentUsername])))
			Expect(opts.AgentPassword).To(Equal(string(secretData[OCISecretAgentPassword])))
			Expect(opts.BasicHTTP).To(BeTrue())
			Expect(opts.InsecureSkipTLS).To(BeTrue())
		})
	})

	When("the given oci storage secret exists with all the non-required fields are not set", func() {
		BeforeEach(func() {
			secretName = "test"
			secretData = map[string][]byte{
				OCISecretReference: []byte("reference"),
			}

			secretType = fleet.SecretTypeOCIStorage
			secretGetErrorMessage = ""
			secretGetNotFoundError = false
		})
		It("returns the expected OCIOpts from the data in the secret", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			opts, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).ToNot(HaveOccurred())
			Expect(opts.Reference).To(Equal(string(secretData[OCISecretReference])))
			Expect(opts.Username).To(BeEmpty())
			Expect(opts.Password).To(BeEmpty())
			Expect(opts.AgentUsername).To(BeEmpty())
			Expect(opts.AgentPassword).To(BeEmpty())
			Expect(opts.BasicHTTP).To(BeFalse())
			Expect(opts.InsecureSkipTLS).To(BeFalse())
		})
	})

	When("the given oci storage secret exists and reference is not set", func() {
		BeforeEach(func() {
			secretName = "test"
			secretData = map[string][]byte{
				OCISecretUsername:      []byte("username"),
				OCISecretPassword:      []byte("password"),
				OCISecretAgentUsername: []byte("agentUsername"),
				OCISecretAgentPassword: []byte("agentPassword"),
				OCISecretBasicHTTP:     []byte("true"),
				OCISecretInsecure:      []byte("true"),
			}

			secretType = fleet.SecretTypeOCIStorage
			secretGetErrorMessage = ""
			secretGetNotFoundError = false
		})
		It("returns an error complaining about reference not being set", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("key \"reference\" not found in secret"))
		})
	})

	When("the given oci storage secret does not exist", func() {
		BeforeEach(func() {
			secretName = "test"
			secretGetNotFoundError = true
			secretGetErrorMessage = ""
		})
		It("returns an error complaining about a secret not being found", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("the given oci storage secret exists but the type is not the expected one", func() {
		BeforeEach(func() {
			secretName = "test"
			secretType = "party-like-its-1999"
			secretGetNotFoundError = false
			secretGetErrorMessage = ""
		})
		It("returns an error complaining about wrong type", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(fmt.Sprintf("unexpected secret type: got %q, want %q", secretType, fleet.SecretTypeOCIStorage)))
		})
	})

	When("there is an error when getting the secret", func() {
		BeforeEach(func() {
			secretName = "test"
			secretGetNotFoundError = false
			secretGetErrorMessage = "SOME ERROR"
		})
		It("returns the error", func() {
			ns := client.ObjectKey{Name: secretName, Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(secretGetErrorMessage))
		})
	})
})

func getSecretFromMockClient(
	mockClient *mocks.MockClient,
	ns types.NamespacedName,
	data map[string][]byte,
	secretType string,
	wantNotFound bool,
	wantErrorMessage string) {
	if wantErrorMessage != "" {
		mockClient.EXPECT().Get(gomock.Any(), ns, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
				return errors.New(wantErrorMessage)
			},
		)
	} else if wantNotFound {
		mockClient.EXPECT().Get(gomock.Any(), ns, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
				return apierrors.NewNotFound(schema.GroupResource{}, "TEST ERROR")
			},
		)
	} else if ns.Name == "" {
		// verify that when the name is not set it uses the default secret name.
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, key types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
				Expect(key.Name).To(Equal(config.DefaultOCIStorageSecretName))
				secret.Data = data
				secret.Type = corev1.SecretType(secretType)
				return nil
			},
		)
	} else if ns.Name != "" {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, key types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
				Expect(ns.Name).To(Equal(key.Name))
				secret.Data = data
				secret.Type = corev1.SecretType(secretType)
				return nil
			},
		)
	}
}
