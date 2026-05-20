package reconciler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestAuthorizeBundle(t *testing.T) {
	dummyErr := errors.New("list failed")

	bundleWithSA := func(sa string) fleet.Bundle {
		return fleet.Bundle{Spec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: sa}}}
	}
	policy := func(p fleet.Policy) *fleet.PolicyList {
		return &fleet.PolicyList{Items: []fleet.Policy{p}}
	}

	cases := []struct {
		name        string
		input       fleet.Bundle
		policies    *fleet.PolicyList
		listErr     error
		expectedErr string
	}{
		{
			name:        "fail when listing policies errors",
			input:       fleet.Bundle{},
			listErr:     dummyErr,
			expectedErr: "list failed",
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

			// Capture original to verify no mutation occurred.
			original := c.input.DeepCopy()
			err := reconciler.AuthorizeBundle(context.TODO(), mockClient, &c.input)

			if c.expectedErr != "" {
				require.Error(t, err)
				assert.Regexp(t, c.expectedErr, err.Error())
			} else {
				require.NoError(t, err)
			}

			// AuthorizeBundle must never mutate the Bundle.
			assert.Equal(t, *original, c.input, "AuthorizeBundle must not mutate the Bundle")
		})
	}
}
