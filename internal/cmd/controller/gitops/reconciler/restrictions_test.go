package reconciler_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
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
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			client := mocks.NewMockClient(mockCtrl)

			client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
				func(_ context.Context, rl *fleet.GitRepoRestrictionList, ns crclient.InNamespace) error {
					if c.restrictions != nil && len(c.restrictions.Items) > 0 {
						rl.Items = c.restrictions.Items
					}

					return c.restrictionsListErr
				},
			)

			err := reconciler.AuthorizeAndAssignDefaults(context.TODO(), client, &c.inputGr)

			if len(c.expectedErr) > 0 {
				require.NotNil(t, err)
				assert.Regexp(t, regexp.MustCompile(c.expectedErr), err.Error())
			} else {
				assert.Nil(t, err)
			}

			if !reflect.DeepEqual(c.inputGr, c.expectedGr) {
				t.Errorf("Expected res %v, got %v", c.expectedGr, c.inputGr)
			}
		})
	}
}
