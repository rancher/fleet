package helmdeployer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func impersonateScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	return scheme
}

func serviceAccount(namespace, name string) client.Object {
	return &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
}

// TestGetServiceAccount covers the resolution that decides whose RBAC gates a
// deployment's downstream writes: an explicitly pinned account is used as-is, an
// unpinned one falls back to "fleet-default" when it exists, and an unpinned one with
// no "fleet-default" resolves to no account at all, which makes the caller run as the
// agent. A pinned account that does not exist is an error rather than that fallback,
// so a typo cannot silently escalate a deployment to the agent's permissions.
func TestGetServiceAccount(t *testing.T) {
	const agentNS = "cattle-fleet-system"

	for _, tc := range []struct {
		name      string
		pinned    string
		existing  []client.Object
		wantNS    string
		wantName  string
		wantError bool
	}{
		{
			name:     "pinned account is used as-is",
			pinned:   "tenant-sa",
			existing: []client.Object{serviceAccount(agentNS, "tenant-sa")},
			wantNS:   agentNS,
			wantName: "tenant-sa",
		},
		{
			name:      "pinned account that does not exist is an error",
			pinned:    "missing-sa",
			existing:  []client.Object{serviceAccount(agentNS, "fleet-default")},
			wantError: true,
		},
		{
			name:     "unpinned falls back to fleet-default when present",
			existing: []client.Object{serviceAccount(agentNS, DefaultServiceAccount)},
			wantNS:   agentNS,
			wantName: DefaultServiceAccount,
		},
		{
			name:     "unpinned resolves to no account without fleet-default",
			existing: nil,
			wantNS:   "",
			wantName: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Helm{
				agentNamespace: agentNS,
				client: fake.NewClientBuilder().
					WithScheme(impersonateScheme(t)).
					WithObjects(tc.existing...).
					Build(),
			}

			ns, name, err := h.getServiceAccount(context.Background(), tc.pinned)
			if tc.wantError {
				require.Error(t, err)
				assert.Empty(t, name, "a failed lookup must not resolve an account")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantNS, ns)
			assert.Equal(t, tc.wantName, name)
		})
	}
}

// TestImpersonatedClient_NoServiceAccount verifies the contract the callers rely on to
// preserve pre-impersonation behaviour: when nothing resolves, the client is nil and
// there is no error, which is what makes them fall back to their own client.
func TestImpersonatedClient_NoServiceAccount(t *testing.T) {
	h := &Helm{
		agentNamespace: "cattle-fleet-system",
		client:         fake.NewClientBuilder().WithScheme(impersonateScheme(t)).Build(),
	}

	c, err := h.ImpersonatedClient(context.Background(), "")
	require.NoError(t, err)
	assert.Nil(t, c, "no resolved service account must not yield an impersonating client")
}

// TestImpersonatedClient_PinnedAccountMissing verifies that a pinned account which does
// not exist downstream fails instead of falling back, so the deployment does not end up
// running with the agent's permissions.
func TestImpersonatedClient_PinnedAccountMissing(t *testing.T) {
	h := &Helm{
		agentNamespace: "cattle-fleet-system",
		client:         fake.NewClientBuilder().WithScheme(impersonateScheme(t)).Build(),
	}

	c, err := h.ImpersonatedClient(context.Background(), "tenant-sa")
	require.Error(t, err)
	assert.Nil(t, c)
}

// TestImpersonationConfig verifies that a resolved account is turned into the matching
// impersonated identity. Building the client itself from this config reads the agent
// pod's token and CA, so it is covered by the e2e test rather than here.
func TestImpersonationConfig(t *testing.T) {
	cfg := impersonationConfig("cattle-fleet-system", "tenant-sa")

	authInfo, ok := cfg.AuthInfos["user"]
	require.True(t, ok, "expected the config to define the impersonating user")
	assert.Equal(t, "system:serviceaccount:cattle-fleet-system:tenant-sa", authInfo.Impersonate)
	assert.Equal(t, "/run/secrets/kubernetes.io/serviceaccount/token", authInfo.TokenFile,
		"the request is authenticated as the agent and only impersonates the account")

	ctx, ok := cfg.Contexts[cfg.CurrentContext]
	require.True(t, ok, "expected the current context to exist")
	assert.Equal(t, "user", ctx.AuthInfo)
}

// TestGetServiceAccountNamespace documents where the account is looked up: the agent
// namespace, falling back to the default namespace only when the agent namespace is
// unset, which happens in some integration tests.
func TestGetServiceAccountNamespace(t *testing.T) {
	assert.Equal(t, "cattle-fleet-system", getServiceAccountNamespace("cattle-fleet-system", "fleet-default"))
	assert.Equal(t, "fleet-default", getServiceAccountNamespace("", "fleet-default"))
}
