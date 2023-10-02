package register

//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/controller_interface_mock.go -package mocks github.com/rancher/wrangler/pkg/generated/controllers/core/v1 Interface,ConfigMapController,SecretController

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/fleet/internal/cmd/agent/mocks"
)

// This is a smoke test, preventing regressions for https://github.com/rancher/rancher/issues/43012 where adding a label
// to cluster registration labels would panic, causing the Fleet agent to crash, if the map of labels was nil.
func TestRunRegistrationLabelSmokeTest(t *testing.T) {
	namespace := "my-namespace"

	ctrl := gomock.NewController(t)
	mockCoreController := mocks.NewMockInterface(ctrl)

	mockSecretController := mocks.NewMockSecretController(ctrl)
	mockCoreController.EXPECT().Secret().Return(mockSecretController)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"apiServerURL":     []byte("https://42.42.42.42:4242"),
			"clusterNamespace": []byte(namespace),
		},
	}
	mockSecretController.EXPECT().Get(namespace, "fleet-agent-bootstrap", metav1.GetOptions{}).Return(&secret, nil)

	mockConfigMapController := mocks.NewMockConfigMapController(ctrl)
	mockConfigMapController.EXPECT().Get(secret.Namespace, "fleet-agent", metav1.GetOptions{}).
		Return(nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}})

	mockCoreController.EXPECT().ConfigMap().Return(mockConfigMapController)

	http.DefaultClient.Timeout = 100 * time.Millisecond // no need to wait longer, the API server URL is a dummy.
	agentSecret, err := runRegistration(context.Background(), mockCoreController, namespace, "my-clusterID")

	assert.Nil(t, agentSecret)
	// expecting a timeout at cluster registration creation time because the API server URL is a dummy.
	assert.Regexp(t, regexp.MustCompile("cannot create clusterregistration.*timeout"), err.Error())
}
