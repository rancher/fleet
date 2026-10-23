package target

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDependencyMatchLabelsTemplates(t *testing.T) {
	for _, repo := range []string{"application-dev", "application-qa"} {
		t.Run(repo, func(t *testing.T) {
			bundle := &fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{Name: repo + "-application", Namespace: "dev", Labels: map[string]string{fleet.RepoLabel: repo}},
				Spec: fleet.BundleSpec{
					Targets: []fleet.BundleTarget{{ClusterSelector: &metav1.LabelSelector{}}},
					DependsOn: []fleet.BundleRef{{
						AcceptedStates: []fleet.BundleState{fleet.Ready, fleet.Modified},
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
							fleet.RepoLabel:            "${ .GitRepo.Name }",
							fleet.BundleNamespaceLabel: "${ .GitRepo.Namespace }",
							"component":                "configs",
							"cluster":                  "${ .ClusterName }",
						}},
					}},
				},
			}
			original := bundle.DeepCopy()
			// Keep source and registration namespaces separate, as in operator-managed tenancy.
			mapping := &fleet.BundleNamespaceMapping{
				ObjectMeta:        metav1.ObjectMeta{Name: "targets", Namespace: "dev"},
				BundleSelector:    &metav1.LabelSelector{},
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "fleet-default"}},
			}
			scheme := newScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bundle.DeepCopy(), mapping,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "fleet-default", Labels: map[string]string{"kubernetes.io/metadata.name": "fleet-default"}}},
				&fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "fleet-default"}},
				&fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "fleet-default"}},
			).Build()
			manager := New(client, client)
			targets, _, err := manager.Targets(t.Context(), bundle, "manifest")
			require.NoError(t, err)
			require.Len(t, targets, 2)
			for _, target := range targets {
				target.Deployment = &fleet.BundleDeployment{ObjectMeta: metav1.ObjectMeta{Name: bundle.Name, Namespace: "cluster-system"}}
				deployment := target.BundleDeployment()
				labels := deployment.Spec.DependsOn[0].Selector.MatchLabels
				require.Equal(t, repo, labels[fleet.RepoLabel])
				require.Equal(t, "dev", labels[fleet.BundleNamespaceLabel])
				require.Equal(t, target.Cluster.Name, labels["cluster"])
				require.Equal(t, "configs", labels["component"])
				require.Equal(t, original.Spec.DependsOn[0].AcceptedStates, deployment.Spec.DependsOn[0].AcceptedStates)
			}
			repeated, _, err := manager.Targets(t.Context(), bundle, "manifest")
			require.NoError(t, err)
			require.Equal(t, targets[0].DependsOn, repeated[0].DependsOn)
			require.Equal(t, original, bundle)
			targets[0].DependsOn[0].Selector.MatchLabels["component"] = "changed"
			require.Equal(t, "configs", targets[1].DependsOn[0].Selector.MatchLabels["component"])
		})
	}
}

func TestDependencyTemplateErrors(t *testing.T) {
	for _, test := range []struct{ name, value, message string }{
		{"missing GitRepo", "${ .GitRepo.Name }", "GitRepo"},
		{"unknown field", "${ .Missing }", "Missing"},
		{"syntax", "${", "parse"},
		{"invalid label", "${ .ClusterName }/invalid", "selector"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dependencies := []fleet.BundleRef{{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"component": test.value}}}}
			_, err := preprocessDependencies(dependencies, map[string]any{"ClusterName": "one"})
			require.ErrorContains(t, err, test.message)
			require.ErrorContains(t, err, "dependsOn[0].selector")
			require.Equal(t, test.value, dependencies[0].Selector.MatchLabels["component"])
		})
	}
}

func TestLiteralDependenciesUnchanged(t *testing.T) {
	dependencies := []fleet.BundleRef{
		{Name: "shared-operator"},
		{Selector: &metav1.LabelSelector{
			MatchLabels:      map[string]string{"component": "configs"},
			MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "instance", Operator: metav1.LabelSelectorOpIn, Values: []string{"literal"}}},
		}},
	}
	result, err := preprocessDependencies(dependencies, nil)
	require.NoError(t, err)
	require.Equal(t, dependencies, result)
	result, err = preprocessDependencies(nil, nil)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestGitRepoContextInHelmValues(t *testing.T) {
	bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{
		Namespace: "dev", Labels: map[string]string{fleet.RepoLabel: "application-qa"},
	}}
	cluster := &fleet.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name: "downstream", Namespace: "fleet-default", Labels: map[string]string{"env": "dev"},
	}}
	opts := fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{Values: &fleet.GenericMap{Data: map[string]any{
		"instance":              "${ .GitRepo.Name }",
		"workspace":             "${ .GitRepo.Namespace }",
		"registrationNamespace": "${ .ClusterNamespace }",
	}}}}
	require.NoError(t, preprocessHelmValues(logr.Discard(), &opts, cluster, newTemplateContext(bundle, cluster)))
	require.Equal(t, map[string]any{
		"instance": "application-qa", "workspace": "dev", "registrationNamespace": "fleet-default",
	}, opts.Helm.Values.Data)
}

func TestDependencyTemplatesPreserveStrings(t *testing.T) {
	dependencies := []fleet.BundleRef{{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
		"boolean": "${ \"true\" }",
		"number":  "${ \"001\" }",
		"sprig":   "${ lower \"CONFIGS\" }",
	}}}}
	result, err := preprocessDependencies(dependencies, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"boolean": "true", "number": "001", "sprig": "configs",
	}, result[0].Selector.MatchLabels)
}
