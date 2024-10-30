package reconciler

import (
	"encoding/json"

	fname "github.com/rancher/fleet/internal/name"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/name"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTargetsConfigMap builds a config map, containing the GitTarget cluster matchers, converted to BundleTargets.
// The BundleTargets are duplicated into TargetRestrictions. TargetRestrictions is a whilelist. A BundleDeployment
// will be created for a Target just if it is inside a TargetRestrictions. If it is not inside TargetRestrictions a Target
// is a TargetCustomization.
func newTargetsConfigMap(repo *fleet.GitRepo) (*corev1.ConfigMap, error) {
	spec := &fleet.BundleSpec{}
	for _, target := range targetsOrDefault(repo.Spec.Targets) {
		spec.Targets = append(spec.Targets, fleet.BundleTarget{
			Name:                 target.Name,
			ClusterName:          target.ClusterName,
			ClusterSelector:      target.ClusterSelector,
			ClusterGroup:         target.ClusterGroup,
			ClusterGroupSelector: target.ClusterGroupSelector,
		})
		spec.TargetRestrictions = append(spec.TargetRestrictions, fleet.BundleTargetRestriction(target))
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}

	hash := fname.KeyHash(string(data))
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.SafeConcatName(repo.Name, "config", hash),
			Namespace: repo.Namespace,
		},
		BinaryData: map[string][]byte{
			"targets.yaml": data,
		},
	}, nil
}

func targetsOrDefault(targets []fleet.GitTarget) []fleet.GitTarget {
	if len(targets) == 0 {
		return []fleet.GitTarget{
			{
				Name:         "default",
				ClusterGroup: "default",
			},
		}
	}
	return targets
}
