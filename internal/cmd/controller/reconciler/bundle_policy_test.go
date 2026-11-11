package reconciler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestAuthorizeBundle(t *testing.T) {
	t.Setenv(experimental.CopyResourcesDownstreamFlag, "true")
	dummyErr := errors.New("list failed")

	bundleWithSA := func(sa string) fleet.Bundle {
		return fleet.Bundle{Spec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: sa}}}
	}
	policy := func(p fleet.Policy) *fleet.PolicyList {
		return &fleet.PolicyList{Items: []fleet.Policy{p}}
	}
	bundleWithDR := func(drs ...fleet.DownstreamResource) fleet.Bundle {
		return fleet.Bundle{Spec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{
			ServiceAccount:      "tenant-sa",
			DownstreamResources: drs,
		}}}
	}
	tenantPolicy := func(allowed ...fleet.AllowedDownstreamResource) *fleet.PolicyList {
		return policy(fleet.Policy{RequireServiceAccount: true, AllowedDownstreamResources: allowed})
	}
	secretRef := fleet.DownstreamResource{Kind: "Secret", Name: "creds"}

	cases := []struct {
		name        string
		input       fleet.Bundle
		policies    *fleet.PolicyList
		listErr     error
		retryable   bool
		expectedErr string
	}{
		{
			name:        "listing policies fails: retryable, not a violation",
			input:       fleet.Bundle{},
			listErr:     dummyErr,
			expectedErr: "failed to list policies in namespace .*: list failed$",
			retryable:   true,
		},
		{
			name:    "no-op when the Policy CRD is not installed",
			input:   bundleWithSA(""),
			listErr: &apimeta.NoKindMatchError{},
		},
		{
			name:     "no-op when no policies exist",
			input:    bundleWithSA("any-sa"),
			policies: &fleet.PolicyList{},
		},
		{
			name:        "require SA: reject when SA is empty",
			input:       fleet.Bundle{},
			policies:    policy(fleet.Policy{RequireServiceAccount: true}),
			expectedErr: "serviceAccount is required",
		},
		{
			name:     "require SA: accept when SA is set",
			input:    bundleWithSA("tenant-sa"),
			policies: policy(fleet.Policy{RequireServiceAccount: true}),
		},
		{
			name:        "allowedServiceAccounts: reject unlisted SA",
			input:       bundleWithSA("bad-sa"),
			policies:    policy(fleet.Policy{AllowedServiceAccounts: []string{"good-sa"}}),
			expectedErr: "disallowed serviceAccount.*",
		},
		{
			name:  "allowedServiceAccounts: accept listed SA",
			input: bundleWithSA("good-sa"),
			policies: policy(fleet.Policy{
				RequireServiceAccount:  true,
				AllowedServiceAccounts: []string{"good-sa"},
			}),
		},
		{
			name: "allowedServiceAccounts: no enforcement when SA is empty and requireServiceAccount is false",
			// SA is empty and requireServiceAccount is false — omitting an SA
			// entirely is allowed even when an allowlist is set.
			input:    fleet.Bundle{},
			policies: policy(fleet.Policy{AllowedServiceAccounts: []string{"good-sa"}}),
		},
		{
			name:  "bundle is never mutated: no SA injection at bundle level",
			input: fleet.Bundle{},
			// Even if a GitRepo sub-object has a default SA, the Bundle reconciler
			// never applies defaults — it only rejects.
			policies: policy(fleet.Policy{
				RequireServiceAccount: false,
				GitRepo:               &fleet.GitRepoPolicySpec{DefaultServiceAccount: "injected-sa"},
			}),
		},
		{
			name:  "multiple policies: requireServiceAccount is OR-ed",
			input: fleet.Bundle{},
			policies: &fleet.PolicyList{Items: []fleet.Policy{
				{RequireServiceAccount: false},
				{RequireServiceAccount: true},
			}},
			expectedErr: "serviceAccount is required",
		},
		{
			name:  "multiple policies: allowedServiceAccounts is union",
			input: bundleWithSA("sa-b"),
			policies: &fleet.PolicyList{Items: []fleet.Policy{
				{AllowedServiceAccounts: []string{"sa-a"}},
				{AllowedServiceAccounts: []string{"sa-b"}},
			}},
		},
		{
			name: "per-target SA: reject unlisted SA in target override",
			input: fleet.Bundle{Spec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: "good-sa"},
				Targets: []fleet.BundleTarget{
					{Name: "prod", BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: "evil-sa"}},
				},
			}},
			policies:    policy(fleet.Policy{AllowedServiceAccounts: []string{"good-sa"}}),
			expectedErr: `disallowed serviceAccount.*target prod`,
		},
		{
			name: "per-target SA: accept listed SA in target override",
			input: fleet.Bundle{Spec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: "good-sa"},
				Targets: []fleet.BundleTarget{
					{Name: "prod", BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: "other-good-sa"}},
				},
			}},
			policies: policy(fleet.Policy{AllowedServiceAccounts: []string{"good-sa", "other-good-sa"}}),
		},
		{
			name: "per-target SA: empty target SA inherits top-level, no double-rejection",
			input: fleet.Bundle{Spec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: "good-sa"},
				Targets: []fleet.BundleTarget{
					{Name: "dev"},
				},
			}},
			policies: policy(fleet.Policy{AllowedServiceAccounts: []string{"good-sa"}}),
		},
		{
			name:     "downstreamResources: unrestricted without requireServiceAccount and allow-list",
			input:    bundleWithDR(secretRef),
			policies: policy(fleet.Policy{AllowedServiceAccounts: []string{"tenant-sa"}}),
		},
		{
			name:        "downstreamResources: requireServiceAccount with an empty allow-list allows nothing",
			input:       bundleWithDR(secretRef),
			policies:    tenantPolicy(),
			expectedErr: "downstream resource Secret creds is not allowed by Policy: it is not listed in allowedDownstreamResources$",
		},
		{
			name:        "downstreamResources: requireServiceAccount with an empty allow-list refuses ConfigMaps",
			input:       bundleWithDR(fleet.DownstreamResource{Kind: "ConfigMap", Name: "cfg"}),
			policies:    tenantPolicy(),
			expectedErr: "downstream resource ConfigMap cfg is not allowed by Policy",
		},
		{
			name:     "downstreamResources: accept Secret listed by name",
			input:    bundleWithDR(secretRef),
			policies: tenantPolicy(fleet.AllowedDownstreamResource{Kind: "Secret", Name: "creds"}),
		},
		{
			name:     "downstreamResources: kind in downstreamResources matches case-insensitively",
			input:    bundleWithDR(fleet.DownstreamResource{Kind: "secret", Name: "creds"}),
			policies: tenantPolicy(fleet.AllowedDownstreamResource{Kind: "Secret", Name: "creds"}),
		},
		{
			name:        "downstreamResources: reject Secret not matching any name",
			input:       bundleWithDR(secretRef),
			policies:    tenantPolicy(fleet.AllowedDownstreamResource{Kind: "Secret", Name: "other"}),
			expectedErr: "downstream resource Secret creds is not allowed by Policy",
		},
		{
			name:        "downstreamResources: an allowed Secret does not allow a ConfigMap of the same name",
			input:       bundleWithDR(fleet.DownstreamResource{Kind: "ConfigMap", Name: "creds"}),
			policies:    tenantPolicy(fleet.AllowedDownstreamResource{Kind: "Secret", Name: "creds"}),
			expectedErr: "downstream resource ConfigMap creds is not allowed by Policy",
		},
		{
			name: "downstreamResources: accept Secret and ConfigMap matching patterns",
			input: bundleWithDR(
				fleet.DownstreamResource{Kind: "Secret", Name: "app-creds"},
				fleet.DownstreamResource{Kind: "ConfigMap", Name: "app-config"},
			),
			policies: tenantPolicy(
				fleet.AllowedDownstreamResource{Kind: "Secret", NamePattern: "app-.*"},
				fleet.AllowedDownstreamResource{Kind: "ConfigMap", NamePattern: "app-.*"},
			),
		},
		{
			name:        "downstreamResources: patterns are anchored",
			input:       bundleWithDR(fleet.DownstreamResource{Kind: "Secret", Name: "my-app-creds"}),
			policies:    tenantPolicy(fleet.AllowedDownstreamResource{Kind: "Secret", NamePattern: "app-.*"}),
			expectedErr: "downstream resource Secret my-app-creds is not allowed by Policy",
		},
		{
			name:        "downstreamResources: an invalid pattern refuses",
			input:       bundleWithDR(secretRef),
			policies:    tenantPolicy(fleet.AllowedDownstreamResource{Kind: "Secret", NamePattern: "("}),
			expectedErr: "downstream resource Secret creds: policy failed to compile regex",
		},
		{
			name:  "downstreamResources: a non-empty allow-list restricts without requireServiceAccount",
			input: bundleWithDR(secretRef),
			policies: policy(fleet.Policy{AllowedDownstreamResources: []fleet.AllowedDownstreamResource{
				{Kind: "Secret", Name: "other"},
			}}),
			expectedErr: "downstream resource Secret creds is not allowed by Policy",
		},
		{
			name:  "downstreamResources: allow-lists of several Policies are unioned",
			input: bundleWithDR(secretRef, fleet.DownstreamResource{Kind: "ConfigMap", Name: "cfg"}),
			policies: &fleet.PolicyList{Items: []fleet.Policy{
				{RequireServiceAccount: true, AllowedDownstreamResources: []fleet.AllowedDownstreamResource{{Kind: "Secret", Name: "creds"}}},
				{AllowedDownstreamResources: []fleet.AllowedDownstreamResource{{Kind: "ConfigMap", Name: "cfg"}}},
			}},
		},
		{
			name: "downstreamResources: reject Secret not allowed in target override",
			input: fleet.Bundle{Spec: fleet.BundleSpec{
				BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: "tenant-sa"},
				Targets: []fleet.BundleTarget{{
					Name: "prod",
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						DownstreamResources: []fleet.DownstreamResource{secretRef},
					},
				}},
			}},
			policies:    tenantPolicy(),
			expectedErr: "downstream resource Secret creds is not allowed by Policy.* in target prod",
		},
		{
			name:     "downstreamResources: unsupported kinds are left to the copy step",
			input:    bundleWithDR(fleet.DownstreamResource{Kind: "Deployment", Name: "app"}),
			policies: tenantPolicy(),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockClient := mocks.NewMockK8sClient(mockCtrl)
			mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
				func(_ context.Context, obj crclient.ObjectList, _ crclient.InNamespace) error {
					if pl, ok := obj.(*fleet.PolicyList); ok {
						if c.listErr != nil {
							return c.listErr
						}
						if c.policies != nil {
							pl.Items = c.policies.Items
						}
					}
					return nil
				},
			)

			// No Get is expected: the check must not read the referenced
			// objects, so that it cannot reveal which of them exist.

			// Capture original to verify no mutation occurred.
			original := c.input.DeepCopy()
			err := reconciler.AuthorizeBundle(context.TODO(), mockClient, &c.input)

			if c.expectedErr != "" {
				require.Error(t, err)
				assert.Regexp(t, c.expectedErr, err.Error())
				assert.Equal(t, c.retryable, errors.Is(err, errorutil.ErrRetryable))
			} else {
				require.NoError(t, err)
			}

			// AuthorizeBundle must never mutate the Bundle.
			assert.Equal(t, *original, c.input, "AuthorizeBundle must not mutate the Bundle")
		})
	}
}

func TestAuthorizeBundleDownstreamResourcesWithoutFlag(t *testing.T) {
	t.Setenv(experimental.CopyResourcesDownstreamFlag, "false")

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(_ context.Context, obj crclient.ObjectList, _ crclient.InNamespace) error {
			if pl, ok := obj.(*fleet.PolicyList); ok {
				pl.Items = []fleet.Policy{{RequireServiceAccount: true}}
			}
			return nil
		},
	)

	bundle := fleet.Bundle{Spec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{
		ServiceAccount:      "tenant-sa",
		DownstreamResources: []fleet.DownstreamResource{{Kind: "Secret", Name: "creds"}},
	}}}

	// Nothing is copied without the experimental flag, so a Bundle naming an
	// object the Policy does not allow is not refused.
	require.NoError(t, reconciler.AuthorizeBundle(context.TODO(), mockClient, &bundle))
}
