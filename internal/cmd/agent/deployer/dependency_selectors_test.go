package deployer

import (
	"testing"

	"github.com/stretchr/testify/require"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCheckDependencyInstanceSelectors(t *testing.T) {
	const workspace, clusterNamespace = "applications", "cluster-downstream"
	scheme := runtime.NewScheme()
	require.NoError(t, fleet.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&fleet.BundleDeployment{}).Build()
	d := Deployer{upstreamClient: c}
	instances := map[string]map[string]*fleet.BundleDeployment{}
	for _, repo := range []string{"application-dev", "application-qa"} {
		instances[repo] = map[string]*fleet.BundleDeployment{}
		for _, component := range []string{"configs", "secrets", "application"} {
			bd := &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: repo + "-" + component, Namespace: clusterNamespace,
					Labels: map[string]string{
						fleet.RepoLabel: repo, fleet.BundleNamespaceLabel: workspace,
						"example.com/component": component,
					},
				},
				Spec:   fleet.BundleDeploymentSpec{DeploymentID: "id", StagedDeploymentID: "id"},
				Status: fleet.BundleDeploymentStatus{Ready: true, NonModified: true, AppliedDeploymentID: "id"},
			}
			if component == "application" {
				for _, prerequisite := range []string{"configs", "secrets"} {
					bd.Spec.DependsOn = append(bd.Spec.DependsOn, fleet.BundleRef{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
							fleet.RepoLabel: repo, fleet.BundleNamespaceLabel: workspace,
							"example.com/component": prerequisite,
						}},
					})
				}
			}
			require.NoError(t, c.Create(t.Context(), bd))
			instances[repo][component] = bd
		}
	}
	dev, qa := instances["application-dev"], instances["application-qa"]
	require.NoError(t, d.checkDependency(t.Context(), dev["application"]))
	require.NoError(t, d.checkDependency(t.Context(), qa["application"]))

	for _, component := range []string{"configs", "secrets"} {
		t.Run(component, func(t *testing.T) {
			dependency := qa[component]
			dependency.Status.Ready = false
			require.NoError(t, c.Status().Update(t.Context(), dependency))
			require.NoError(t, d.checkDependency(t.Context(), dev["application"]))
			var pending *NotReadyDependenciesError
			require.ErrorAs(t, d.checkDependency(t.Context(), qa["application"]), &pending)
			require.Len(t, pending.Pending, 1)
			require.Equal(t, dependency.Name, pending.Pending[0].Name)
			dependency.Status.Ready = true
			require.NoError(t, c.Status().Update(t.Context(), dependency))
			require.NoError(t, d.checkDependency(t.Context(), qa["application"]))
		})
	}

	// A ready dev prerequisite must not satisfy a missing QA prerequisite.
	require.NoError(t, c.Delete(t.Context(), qa["secrets"]))
	require.ErrorContains(t, d.checkDependency(t.Context(), qa["application"]), "no bundles matching labels")
	require.NoError(t, d.checkDependency(t.Context(), dev["application"]))
}
