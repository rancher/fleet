package reconciler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/controller/helmops/reconciler"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestHelmOpAuthorizeAndAssignDefaults(t *testing.T) {
	dummyErr := errors.New("list failed")

	helmopWithSA := func(sa string) fleet.HelmOp {
		return fleet.HelmOp{Spec: fleet.HelmOpSpec{BundleSpec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{ServiceAccount: sa}}}}
	}
	helmopWithRepo := func(repo string) fleet.HelmOp {
		return fleet.HelmOp{Spec: fleet.HelmOpSpec{BundleSpec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{Repo: repo}}}}}
	}
	helmopWithChart := func(chart string) fleet.HelmOp {
		return fleet.HelmOp{Spec: fleet.HelmOpSpec{BundleSpec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{Chart: chart}}}}}
	}
	helmopWithSecret := func(secret string) fleet.HelmOp {
		return fleet.HelmOp{Spec: fleet.HelmOpSpec{HelmSecretName: secret}}
	}

	policy := func(spec fleet.Policy) *fleet.PolicyList {
		return &fleet.PolicyList{Items: []fleet.Policy{spec}}
	}

	cases := []struct {
		name        string
		input       fleet.HelmOp
		policies    *fleet.PolicyList
		listErr     error
		expected    fleet.HelmOp
		expectedErr string
	}{
		{
			name:        "fail when listing policies errors",
			input:       fleet.HelmOp{},
			listErr:     dummyErr,
			expected:    fleet.HelmOp{},
			expectedErr: "list failed",
		},
		{
			name:     "no-op when no policies exist",
			input:    helmopWithSA("any-sa"),
			policies: &fleet.PolicyList{},
			expected: helmopWithSA("any-sa"),
		},
		{
			name:  "require SA: reject when SA is empty",
			input: fleet.HelmOp{},
			policies: policy(fleet.Policy{
				RequireServiceAccount: true,
			}),
			expected:    fleet.HelmOp{},
			expectedErr: "serviceAccount is required",
		},
		{
			name:  "require SA: inject default SA from helmOp sub-object, then pass",
			input: fleet.HelmOp{},
			policies: policy(fleet.Policy{
				RequireServiceAccount: true,
				HelmOp: &fleet.HelmOpPolicySpec{
					DefaultServiceAccount: "injected-sa",
				},
			}),
			expected: helmopWithSA("injected-sa"),
		},
		{
			name:  "allowedServiceAccounts: reject unlisted SA",
			input: helmopWithSA("bad-sa"),
			policies: policy(fleet.Policy{
				AllowedServiceAccounts: []string{"good-sa"},
			}),
			expected:    helmopWithSA("bad-sa"),
			expectedErr: "disallowed serviceAccount.*",
		},
		{
			name:  "allowedServiceAccounts: accept listed SA",
			input: helmopWithSA("good-sa"),
			policies: policy(fleet.Policy{
				RequireServiceAccount:  true,
				AllowedServiceAccounts: []string{"good-sa"},
			}),
			expected: helmopWithSA("good-sa"),
		},
		{
			name:  "allowedHelmSecretNames: inject default and accept",
			input: fleet.HelmOp{},
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					DefaultHelmSecretName:  "default-secret",
					AllowedHelmSecretNames: []string{"default-secret"},
				},
			}),
			expected: helmopWithSecret("default-secret"),
		},
		{
			name:  "allowedHelmSecretNames: reject unlisted secret",
			input: helmopWithSecret("bad-secret"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedHelmSecretNames: []string{"good-secret"},
				},
			}),
			expected:    helmopWithSecret("bad-secret"),
			expectedErr: "disallowed helmSecretName.*",
		},
		{
			name:  "allowedHelmRepoPatterns: reject non-matching repo",
			input: helmopWithRepo("https://evil.example.com/charts"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedHelmRepoPatterns: []string{"^https://charts\\.tenant-a\\.example\\.com/.*"},
				},
			}),
			expected:    helmopWithRepo("https://evil.example.com/charts"),
			expectedErr: "disallowed helm repo.*",
		},
		{
			name:  "allowedHelmRepoPatterns: accept matching repo",
			input: helmopWithRepo("https://charts.tenant-a.example.com/stable"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedHelmRepoPatterns: []string{"^https://charts\\.tenant-a\\.example\\.com/.*"},
				},
			}),
			expected: helmopWithRepo("https://charts.tenant-a.example.com/stable"),
		},
		{
			name:  "allowedChartPatterns: reject non-matching chart",
			input: helmopWithChart("malicious-chart"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedChartPatterns: []string{"^tenant-a-.*"},
				},
			}),
			expected:    helmopWithChart("malicious-chart"),
			expectedErr: "disallowed helm chart.*",
		},
		{
			name:  "allowedChartPatterns: accept matching chart",
			input: helmopWithChart("tenant-a-app"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedChartPatterns: []string{"^tenant-a-.*"},
				},
			}),
			expected: helmopWithChart("tenant-a-app"),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockClient := mocks.NewMockClient(mockCtrl)
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

			input := c.input.DeepCopy()
			err := reconciler.AuthorizeAndAssignDefaults(context.TODO(), mockClient, input)

			if c.expectedErr != "" {
				require.Error(t, err)
				assert.Regexp(t, c.expectedErr, err.Error())
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, c.expected, *input)
		})
	}
}
