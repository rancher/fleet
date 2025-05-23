package ocistorage

import (
	"context"
	"errors"
	"fmt"

	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

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
		ctrl                          *gomock.Controller
		secretGetErrorMessage         string
		secretGetNotFoundError        bool
		defaultSecretGetErrorMessage  string
		defaultSecretGetNotFoundError bool
		mockClient                    *mocks.MockClient
		secretData                    map[string][]byte
		defaultSecretData             map[string][]byte
		secretType                    string
		defaultSecretType             string
	)

	JustBeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockClient = mocks.NewMockClient(ctrl)
		ns := types.NamespacedName{Name: "test", Namespace: "test"}
		getSecretFromMockClient(
			mockClient,
			ns,
			secretData,
			secretType,
			secretGetNotFoundError,
			secretGetErrorMessage,
		)
		if secretGetNotFoundError {
			ns = types.NamespacedName{Name: DefaultSecretNameOCIStorage, Namespace: "test"}
			getSecretFromMockClient(
				mockClient,
				ns,
				defaultSecretData,
				defaultSecretType,
				defaultSecretGetNotFoundError,
				defaultSecretGetErrorMessage,
			)
		}
	})

	When("the given oci storage secret exists with all fields set", func() {
		BeforeEach(func() {
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
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
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
			secretData = map[string][]byte{
				OCISecretReference: []byte("reference"),
			}

			secretType = fleet.SecretTypeOCIStorage
			secretGetErrorMessage = ""
			secretGetNotFoundError = false
		})
		It("returns the expected OCIOpts from the data in the secret", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
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
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("key \"reference\" not found in secret"))
		})
	})

	When("the given oci storage secret does not exists but the default one does", func() {
		BeforeEach(func() {
			defaultSecretData = map[string][]byte{
				OCISecretReference:     []byte("defaultReference"),
				OCISecretUsername:      []byte("defaultUsername"),
				OCISecretPassword:      []byte("defaultPassword"),
				OCISecretAgentUsername: []byte("defaultAgentUsername"),
				OCISecretAgentPassword: []byte("defaultAgentPassword"),
				OCISecretBasicHTTP:     []byte("true"),
				OCISecretInsecure:      []byte("true"),
			}

			defaultSecretType = fleet.SecretTypeOCIStorage
			secretGetNotFoundError = true
			secretGetErrorMessage = ""

			defaultSecretGetErrorMessage = ""
			defaultSecretGetNotFoundError = false
		})
		It("returns an error complaining about reference not being set", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			opts, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).ToNot(HaveOccurred())
			Expect(opts.Reference).To(Equal(string(defaultSecretData[OCISecretReference])))
			Expect(opts.Username).To(Equal(string(defaultSecretData[OCISecretUsername])))
			Expect(opts.Password).To(Equal(string(defaultSecretData[OCISecretPassword])))
			Expect(opts.AgentUsername).To(Equal(string(defaultSecretData[OCISecretAgentUsername])))
			Expect(opts.AgentPassword).To(Equal(string(defaultSecretData[OCISecretAgentPassword])))
			Expect(opts.BasicHTTP).To(BeTrue())
			Expect(opts.InsecureSkipTLS).To(BeTrue())
		})
	})

	When("the given oci storage secret and the default one dont exist", func() {
		BeforeEach(func() {
			secretGetNotFoundError = true
			secretGetErrorMessage = ""

			defaultSecretGetErrorMessage = ""
			defaultSecretGetNotFoundError = true
		})
		It("returns an error complaining about reference not being set", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("the given oci storage secret exists but the type is not the expected one", func() {
		BeforeEach(func() {
			secretType = "party-like-its-1999"
			secretGetNotFoundError = false
			secretGetErrorMessage = ""
		})
		It("returns an error complaining about wrong type", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(fmt.Sprintf("unexpected secret type: got %q, want %q", secretType, fleet.SecretTypeOCIStorage)))
		})
	})

	When("the given oci storage secret does not exist and the default one does, but with the wrong type", func() {
		BeforeEach(func() {
			defaultSecretType = "party-like-its-1999"
			secretGetNotFoundError = true
			secretGetErrorMessage = ""

			defaultSecretGetErrorMessage = ""
			defaultSecretGetNotFoundError = false
		})
		It("returns an error complaining about wrong type", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(fmt.Sprintf("unexpected secret type: got %q, want %q", defaultSecretType, fleet.SecretTypeOCIStorage)))
		})
	})

	When("there is an error when getting the secret", func() {
		BeforeEach(func() {
			secretGetNotFoundError = false
			secretGetErrorMessage = "SOME ERROR"
		})
		It("returns the error", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(secretGetErrorMessage))
		})
	})

	When("there is an error when getting the default secret", func() {
		BeforeEach(func() {
			secretGetNotFoundError = true
			secretGetErrorMessage = ""

			defaultSecretGetErrorMessage = "SOME ERROR GETTING THE DEFAULT SECRET"
			defaultSecretGetNotFoundError = false
		})
		It("returns the error", func() {
			ns := types.NamespacedName{Name: "test", Namespace: "test"}
			_, err := ReadOptsFromSecret(context.TODO(), mockClient, ns)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(defaultSecretGetErrorMessage))
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
	} else {
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
				secret.Data = data
				secret.Type = corev1.SecretType(secretType)
				return nil
			},
		)
	}
}
