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

func PodDisruptionBudget(agentNamespace string, podDisruptionBudgetSpec *fleet.PodDisruptionBudgetSpec) (*policyv1.PodDisruptionBudget, error) {
	pdbSpec := policyv1.PodDisruptionBudgetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "fleet-agent"},
		},
	}

	if podDisruptionBudgetSpec.MaxUnavailable == "" && podDisruptionBudgetSpec.MinAvailable == "" {
		logrus.Warnf("Neither MaxUnavailable nor MinAvailable is set, defaulting to 0 for MaxUnavailable")
		pdbSpec.MaxUnavailable = &intstr.IntOrString{IntVal: 0}
	} else if podDisruptionBudgetSpec.MaxUnavailable != "" && podDisruptionBudgetSpec.MinAvailable != "" {
		return &policyv1.PodDisruptionBudget{},
			fmt.Errorf("both MaxUnavailable (%s) and MinAvailable (%s) are set, not creating PDB", podDisruptionBudgetSpec.MaxUnavailable, podDisruptionBudgetSpec.MinAvailable)
	} else if podDisruptionBudgetSpec.MaxUnavailable != "" {
		mu := intstr.Parse(podDisruptionBudgetSpec.MaxUnavailable)
		pdbSpec.MaxUnavailable = &mu
	} else if podDisruptionBudgetSpec.MinAvailable != "" {
		mu := intstr.Parse(podDisruptionBudgetSpec.MinAvailable)
		pdbSpec.MinAvailable = &mu
	}

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      FleetAgentPodDisruptionBudgetName,
			Namespace: agentNamespace,
		},
		Spec: pdbSpec,
	}, nil
}
