package helmdeployer

import (
	"strings"
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/google/go-cmp/cmp"
)

func TestYieldSourceOrigins(t *testing.T) {
	tests := map[string]struct {
		bdLabels         map[string]string
		expectedIdentity sourceIdentity
		expectedOK       bool
	}{
		"gitrepo source": {
			bdLabels: map[string]string{
				v1alpha1.RepoLabel:            "label-trace",
				v1alpha1.BundleNamespaceLabel: "fleet-default",
			},
			expectedIdentity: sourceIdentity{
				Kind:      v1alpha1.ManagedByKindGitRepo,
				Namespace: "fleet-default",
				Name:      "label-trace",
			},
			expectedOK: true,
		},
		"gitrepo source alongside unrelated labels": {
			bdLabels: map[string]string{
				v1alpha1.RepoLabel:             "label-trace",
				v1alpha1.BundleNamespaceLabel:  "fleet-local",
				v1alpha1.BundleLabel:           "label-trace-simple",
				"objectset.rio.cattle.io/hash": "abc123",
			},
			expectedIdentity: sourceIdentity{
				Kind:      v1alpha1.ManagedByKindGitRepo,
				Namespace: "fleet-local",
				Name:      "label-trace",
			},
			expectedOK: true,
		},
		"helmop source": {
			bdLabels: map[string]string{
				v1alpha1.HelmOpLabel:          "kafka",
				v1alpha1.BundleNamespaceLabel: "fleet-default",
			},
			expectedIdentity: sourceIdentity{
				Kind:      v1alpha1.ManagedByKindHelmOp,
				Namespace: "fleet-default",
				Name:      "kafka",
			},
			expectedOK: true,
		},
		// Defensive: a BundleDeployment carrying both source labels is not
		// expected. Pin GitRepo as the winner so the outcome is deterministic.
		"both source labels present prefers gitrepo": {
			bdLabels: map[string]string{
				v1alpha1.RepoLabel:            "label-trace",
				v1alpha1.HelmOpLabel:          "kafka",
				v1alpha1.BundleNamespaceLabel: "fleet-default",
			},
			expectedIdentity: sourceIdentity{
				Kind:      v1alpha1.ManagedByKindGitRepo,
				Namespace: "fleet-default",
				Name:      "label-trace",
			},
			expectedOK: true,
		},
		"fleet's own internal bundle carries no source labels": {
			bdLabels: map[string]string{
				"objectset.rio.cattle.io/hash": "abc123",
			},
			expectedOK: false,
		},
		"no labels at all": {
			bdLabels:   nil,
			expectedOK: false,
		},
		"source label present but namespace missing": {
			bdLabels: map[string]string{
				v1alpha1.RepoLabel: "label-trace",
			},
			expectedOK: false,
		},
		"empty source label value is not propagated": {
			bdLabels: map[string]string{
				v1alpha1.RepoLabel:            "",
				v1alpha1.BundleNamespaceLabel: "fleet-default",
			},
			expectedOK: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			identity, ok := yieldSourceOrigins(test.bdLabels)
			if ok != test.expectedOK {
				t.Fatalf("expected ok %v, got %v", test.expectedOK, ok)
			}
			if !ok {
				return
			}
			if !cmp.Equal(identity, test.expectedIdentity) {
				t.Errorf("expected %+v, got %+v", test.expectedIdentity, identity)
			}
		})
	}
}

func TestSourceIdentityLabels(t *testing.T) {
	tests := map[string]struct {
		identity       sourceIdentity
		expectedLabels map[string]string
	}{
		"name under the limit is used verbatim": {
			identity: sourceIdentity{
				Kind:      v1alpha1.ManagedByKindGitRepo,
				Namespace: "fleet-default",
				Name:      "label-trace",
			},
			expectedLabels: map[string]string{
				v1alpha1.ManagedByKindLabel:      "gitrepo",
				v1alpha1.ManagedByNamespaceLabel: "fleet-default",
				v1alpha1.ManagedByNameLabel:      "label-trace",
			},
		},
		"name of exactly 63 characters is used verbatim": {
			identity: sourceIdentity{
				Kind:      v1alpha1.ManagedByKindHelmOp,
				Namespace: "fleet-local",
				Name:      strings.Repeat("a", 63),
			},
			expectedLabels: map[string]string{
				v1alpha1.ManagedByKindLabel:      "helmop",
				v1alpha1.ManagedByNamespaceLabel: "fleet-local",
				v1alpha1.ManagedByNameLabel:      strings.Repeat("a", 63),
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.identity.labels(); !cmp.Equal(got, test.expectedLabels) {
				t.Errorf("expected %v, got %v", test.expectedLabels, got)
			}
		})
	}
}

func TestSourceIdentityLabelsShortening(t *testing.T) {
	identityFor := func(sourceName string) sourceIdentity {
		return sourceIdentity{
			Kind:      v1alpha1.ManagedByKindGitRepo,
			Namespace: "fleet-default",
			Name:      sourceName,
		}
	}

	longName := strings.Repeat("a", 64)
	shortened := identityFor(longName).labels()[v1alpha1.ManagedByNameLabel]

	if len(shortened) > maxLabelValueLength {
		t.Errorf("expected shortened name within %d characters, got %d: %q",
			maxLabelValueLength, len(shortened), shortened)
	}
	if shortened == longName {
		t.Errorf("expected name over the limit to be shortened, got it verbatim")
	}

	// Deterministic: an unchanged source yields an unchanged value.
	if again := identityFor(longName).labels()[v1alpha1.ManagedByNameLabel]; again != shortened {
		t.Errorf("expected shortening to be deterministic, got %q then %q", shortened, again)
	}

	// Distinct long names must not collide. These two share the first
	// 63 characters, so only the appended digest can tell them apart.
	first := identityFor(strings.Repeat("b", 63) + "one").labels()[v1alpha1.ManagedByNameLabel]
	second := identityFor(strings.Repeat("b", 63) + "two").labels()[v1alpha1.ManagedByNameLabel]
	if first == second {
		t.Errorf("expected distinct long names to yield distinct labels, both gave %q", first)
	}
}
