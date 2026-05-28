package reconciler_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestAuthorizeAndAssignDefaults(t *testing.T) {
	dummyErrMsg := "something happened"
	dummyErr := errors.New(dummyErrMsg)
	cases := []struct {
		name                string
		inputGr             fleet.GitRepo
		restrictions        *fleet.GitRepoRestrictionList
		policies            *fleet.PolicyList
		restrictionsListErr error
		expectedGr          fleet.GitRepo
		expectedErr         string
	}{
		{
			name:                "fail when listing GitRepo restrictions errors",
			inputGr:             fleet.GitRepo{},
			restrictionsListErr: dummyErr,
			expectedGr:          fleet.GitRepo{},
			expectedErr:         dummyErrMsg,
		},
		{
			name:         "pass when list of GitRepo restrictions is empty",
			inputGr:      fleet.GitRepo{},
			restrictions: &fleet.GitRepoRestrictionList{},
			expectedGr:   fleet.GitRepo{},
		},
		{
			name:    "deny empty targetNamespace when allowedTargetNamespaces restriction present",
			inputGr: fleet.GitRepo{}, // no target ns provided
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedTargetNamespaces: []string{"foo"},
					},
				},
			},
			expectedGr:  fleet.GitRepo{},
			expectedErr: "empty targetNamespace denied.*allowedTargetNamespaces restriction is present",
		},
		{
			name: "deny disallowed targetNamespace",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					TargetNamespace: "not-foo",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedTargetNamespaces: []string{"foo"},
					},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					TargetNamespace: "not-foo",
				},
			},
			expectedErr: "disallowed targetNamespace.*",
		},
		{
			name: "deny disallowed service account",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ServiceAccount: "not-foo",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedServiceAccounts: []string{"foo"},
					},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ServiceAccount: "not-foo",
				},
			},
			expectedErr: "disallowed serviceAccount.*",
		},
		{
			name: "deny disallowed repo pattern",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "bar",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedRepoPatterns: []string{"baz"},
					},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "bar",
				},
			},
			expectedErr: "disallowed repo.*",
		},
		{
			name: "deny disallowed client secret name",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ClientSecretName: "not-foo",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedClientSecretNames: []string{"foo"},
					},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ClientSecretName: "not-foo",
				},
			},
			expectedErr: "disallowed clientSecretName.*",
		},
		{
			name: "pass when no restrictions nor disallowed values exist",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "http://foo.bar/baz",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "http://foo.bar/baz",
				},
			},
		},
		{
			name: "pass when restrictions exist and the GitRepo matches allowed values",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ClientSecretName: "csn",
					Repo:             "http://foo.bar/baz",
					ServiceAccount:   "sacc",
					TargetNamespace:  "tns",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedClientSecretNames: []string{"csn"},
						AllowedRepoPatterns:      []string{".*foo.bar.*"},
						AllowedServiceAccounts:   []string{"sacc"},
						AllowedTargetNamespaces:  []string{"tns"},
					},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ClientSecretName: "csn",
					Repo:             "http://foo.bar/baz",
					ServiceAccount:   "sacc",
					TargetNamespace:  "tns",
				},
			},
		},
		{
			name: "pass and mutate repo with defaults when restrictions exist and the GitRepo matches allowed values",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo:            "http://foo.bar/baz",
					TargetNamespace: "tns",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{
						AllowedClientSecretNames: []string{"csn"},
						AllowedRepoPatterns:      []string{".*foo.bar.*"},
						AllowedServiceAccounts:   []string{"sacc"},
						AllowedTargetNamespaces:  []string{"tns"},
						DefaultClientSecretName:  "dcsn",
						DefaultServiceAccount:    "dsacc",
					},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					ClientSecretName: "dcsn",
					Repo:             "http://foo.bar/baz",
					ServiceAccount:   "dsacc",
					TargetNamespace:  "tns",
				},
			},
		},
		{
			// Policy patterns are anchored: a pattern without .* must match the full URL.
			name: "Policy repo pattern: reject URL that matches only as substring",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "https://evil.example.com/redirect?to=github.com/myorg/repo",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{},
			policies: &fleet.PolicyList{Items: []fleet.Policy{
				{GitRepo: &fleet.GitRepoPolicySpec{
					AllowedRepoPatterns: []string{`github\.com/myorg/.*`},
				}},
			}},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "https://evil.example.com/redirect?to=github.com/myorg/repo",
				},
			},
			expectedErr: "disallowed repo.*",
		},
		{
			// Policy patterns are anchored: the pattern must match the full URL.
			name: "Policy repo pattern: accept URL that matches anchored pattern",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "https://github.com/myorg/myrepo",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{},
			policies: &fleet.PolicyList{Items: []fleet.Policy{
				{GitRepo: &fleet.GitRepoPolicySpec{
					AllowedRepoPatterns: []string{`https://github\.com/myorg/.*`},
				}},
			}},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "https://github.com/myorg/myrepo",
				},
			},
		},
		{
			// GitRepoRestriction patterns remain unanchored for backward compat.
			name: "GitRepoRestriction repo pattern: unanchored match still works",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "http://foo.bar/baz",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{AllowedRepoPatterns: []string{`foo\.bar`}},
				},
			},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "http://foo.bar/baz",
				},
			},
		},
		{
			// A URL rejected by the Policy list but accepted by a GRR pattern should pass.
			name: "GRR allows repo even when Policy list would reject it",
			inputGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "https://github.com/myorg/myrepo",
				},
			},
			restrictions: &fleet.GitRepoRestrictionList{
				Items: []fleet.GitRepoRestriction{
					{AllowedRepoPatterns: []string{`.*github\.com.*`}},
				},
			},
			policies: &fleet.PolicyList{Items: []fleet.Policy{
				{GitRepo: &fleet.GitRepoPolicySpec{
					// Anchored pattern that does NOT match the full URL.
					AllowedRepoPatterns: []string{`gitlab\.com/.*`},
				}},
			}},
			expectedGr: fleet.GitRepo{
				Spec: fleet.GitRepoSpec{
					Repo: "https://github.com/myorg/myrepo",
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			client := mocks.NewMockK8sClient(mockCtrl)

			client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
				func(_ context.Context, obj crclient.ObjectList, ns crclient.InNamespace) error {
					if rl, ok := obj.(*fleet.GitRepoRestrictionList); ok {
						if c.restrictions != nil && len(c.restrictions.Items) > 0 {
							rl.Items = c.restrictions.Items
						}
						return c.restrictionsListErr
					}
					if pl, ok := obj.(*fleet.PolicyList); ok && c.policies != nil {
						pl.Items = c.policies.Items
					}
					return nil
				},
			)

			err := reconciler.AuthorizeAndAssignDefaults(context.TODO(), client, &c.inputGr)

			if len(c.expectedErr) > 0 {
				require.Error(t, err)
				assert.Regexp(t, c.expectedErr, err.Error())
			} else {
				require.NoError(t, err)
			}

			if !reflect.DeepEqual(c.inputGr, c.expectedGr) {
				t.Errorf("Expected res %v, got %v", c.expectedGr, c.inputGr)
			}
		})
	}
}
