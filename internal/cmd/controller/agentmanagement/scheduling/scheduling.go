package scheduling

import (
	"fmt"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	FleetAgentPriorityClassName       = "fleet-agent-priority-class"
	FleetAgentPodDisruptionBudgetName = "fleet-agent-pod-disruption-budget"
)

func PriorityClass(priorityClass *fleet.PriorityClassSpec) *schedulingv1.PriorityClass {
	return &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: FleetAgentPriorityClassName,
		},
		Value:            int32(priorityClass.Value),
		Description:      "Priority class for Fleet Agent",
		PreemptionPolicy: priorityClass.PreemptionPolicy,
	}
}

func PodDisruptionBudget(agentNamespace string, pdbs *fleet.PodDisruptionBudgetSpec) (*policyv1.PodDisruptionBudget, error) {
	pdbSpec := policyv1.PodDisruptionBudgetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "fleet-agent"},
		},
	}

	if pdbs.MaxUnavailable == "" && pdbs.MinAvailable == "" {
		logrus.Warnf("Neither MaxUnavailable nor MinAvailable is set, defaulting to 0 for MaxUnavailable")
		pdbSpec.MaxUnavailable = &intstr.IntOrString{IntVal: 0}
	} else if pdbs.MaxUnavailable != "" && (pdbs.MinAvailable == "" || pdbs.MinAvailable == "0") {
		mu := intstr.Parse(pdbs.MaxUnavailable)
		pdbSpec.MaxUnavailable = &mu
	} else if pdbs.MinAvailable != "" && (pdbs.MaxUnavailable == "" || pdbs.MaxUnavailable == "0") {
		ma := intstr.Parse(pdbs.MinAvailable)
		pdbSpec.MinAvailable = &ma
	} else if pdbs.MaxUnavailable != "" && pdbs.MinAvailable != "" {
		return &policyv1.PodDisruptionBudget{},
			fmt.Errorf("both MaxUnavailable (%s) and MinAvailable (%s) are set, not creating PDB", pdbs.MaxUnavailable, pdbs.MinAvailable)
	}

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      FleetAgentPodDisruptionBudgetName,
			Namespace: agentNamespace,
		},
		Spec: pdbSpec,
	}, nil
}
