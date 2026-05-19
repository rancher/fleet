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

func TestHelmAppAuthorizeAndAssignDefaults(t *testing.T) {
	dummyErr := errors.New("list failed")

	helmappWithRepo := func(repo string) fleet.HelmApp {
		return fleet.HelmApp{Spec: fleet.HelmAppSpec{BundleSpec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{Repo: repo}}}}}
	}
	helmappWithChart := func(chart string) fleet.HelmApp {
		return fleet.HelmApp{Spec: fleet.HelmAppSpec{BundleSpec: fleet.BundleSpec{BundleDeploymentOptions: fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{Chart: chart}}}}}
	}
	helmappWithSecret := func(secret string) fleet.HelmApp {
		return fleet.HelmApp{Spec: fleet.HelmAppSpec{HelmSecretName: secret}}
	}

	policy := func(spec fleet.Policy) *fleet.PolicyList {
		return &fleet.PolicyList{Items: []fleet.Policy{spec}}
	}

	cases := []struct {
		name        string
		input       fleet.HelmApp
		policies    *fleet.PolicyList
		listErr     error
		expected    fleet.HelmApp
		expectedErr string
	}{
		{
			name:        "fail when listing policies errors",
			input:       fleet.HelmApp{},
			listErr:     dummyErr,
			expected:    fleet.HelmApp{},
			expectedErr: "list failed",
		},
		{
			name:     "no-op when no policies exist",
			input:    helmappWithSecret("any-secret"),
			policies: &fleet.PolicyList{},
			expected: helmappWithSecret("any-secret"),
		},
		{
			name:  "allowedHelmSecretNames: inject default and accept",
			input: fleet.HelmApp{},
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					DefaultHelmSecretName:  "default-secret",
					AllowedHelmSecretNames: []string{"default-secret"},
				},
			}),
			expected: helmappWithSecret("default-secret"),
		},
		{
			name:  "allowedHelmSecretNames: reject unlisted secret",
			input: helmappWithSecret("bad-secret"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedHelmSecretNames: []string{"good-secret"},
				},
			}),
			expected:    helmappWithSecret("bad-secret"),
			expectedErr: "disallowed helmSecretName.*",
		},
		{
			name:  "allowedHelmRepoPatterns: reject non-matching repo",
			input: helmappWithRepo("https://evil.example.com/charts"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedHelmRepoPatterns: []string{"^https://charts\\.tenant-a\\.example\\.com/.*"},
				},
			}),
			expected:    helmappWithRepo("https://evil.example.com/charts"),
			expectedErr: "disallowed helm repo.*",
		},
		{
			name:  "allowedHelmRepoPatterns: accept matching repo",
			input: helmappWithRepo("https://charts.tenant-a.example.com/stable"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedHelmRepoPatterns: []string{"^https://charts\\.tenant-a\\.example\\.com/.*"},
				},
			}),
			expected: helmappWithRepo("https://charts.tenant-a.example.com/stable"),
		},
		{
			name:  "allowedChartPatterns: reject non-matching chart",
			input: helmappWithChart("malicious-chart"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedChartPatterns: []string{"^tenant-a-.*"},
				},
			}),
			expected:    helmappWithChart("malicious-chart"),
			expectedErr: "disallowed helm chart.*",
		},
		{
			name:  "allowedChartPatterns: accept matching chart",
			input: helmappWithChart("tenant-a-app"),
			policies: policy(fleet.Policy{
				HelmOp: &fleet.HelmOpPolicySpec{
					AllowedChartPatterns: []string{"^tenant-a-.*"},
				},
			}),
			expected: helmappWithChart("tenant-a-app"),
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
