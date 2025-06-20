package k8sclient

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

// GetObjectShouldSucceed gets the object identified by name and namespace and ensures it succeeds
func GetObjectShouldSucceed(c client.Client, name, namespace string, obj client.Object) {
	Eventually(func(g Gomega) {
		err := c.Get(context.TODO(), client.ObjectKey{Name: name, Namespace: namespace}, obj)
		g.Expect(err).ToNot(HaveOccurred())
	}).Should(Succeed())
}

// DeleteObjectShouldSucceed deletes the given object and ensures it succeeds
func DeleteObjectShouldSucceed(c client.Client, obj client.Object) {
	err := c.Delete(context.TODO(), obj)
	Expect(err).ToNot(HaveOccurred())
}

// CreateObjectShouldSucceed creates the given object and ensures it succeeds
func CreateObjectShouldSucceed(c client.Client, obj client.Object) {
	err := c.Create(context.TODO(), obj)
	Expect(err).ToNot(HaveOccurred())
}

// UpdateObjectShouldSucceed updates the given object and ensures it succeeds
func UpdateObjectShouldSucceed(c client.Client, obj client.Object) {
	err := c.Update(context.TODO(), obj)
	Expect(err).ToNot(HaveOccurred())
}

// ObjectShouldNotExist checks the object identified with name and namespace does not exist.
// If checkConsistently is set to true it checks consistently for 5 seconds, otherwise it runs a single check
func ObjectShouldNotExist(c client.Client, name, namespace string, obj client.Object, checkConsistently bool) {
	if checkConsistently {
		Consistently(func(g Gomega) {
			err := c.Get(context.TODO(), client.ObjectKey{Name: name, Namespace: namespace}, obj)
			g.Expect(errors.IsNotFound(err)).To(BeTrue())
		}, 5*time.Second, 1*time.Second).Should(Succeed())
	} else {
		err := c.Get(context.TODO(), client.ObjectKey{Name: name, Namespace: namespace}, obj)
		Expect(errors.IsNotFound(err)).To(BeTrue())
	}
}

// GetPodEnvVariable returns the value of the given env variable in the first container of the first pod
// that matches the given labelSelector.
// Returns the pod name, the value of the env variable and error (if any).
// Returns a NotFound error in case of not finding any matching pod or if the first pod has no containers.
// Returns the value of the env variable or "unset" if it was not found in the container.
func GetPodEnvVariable(c client.Client, envVarName string, labelSelector client.MatchingLabels) (string, string, error) {
	podList := &corev1.PodList{}
	err := c.List(context.TODO(), podList, labelSelector)
	if err != nil {
		return "", "", err
	}
	if len(podList.Items) == 0 {
		return "", "", errors.NewNotFound(corev1.Resource("pods"), "no pods found")
	}

	pod := podList.Items[0]
	if len(pod.Spec.Containers) == 0 {
		return "", "", errors.NewNotFound(corev1.Resource("containers"), "no containers found")
	}

	envVars := pod.Spec.Containers[0].Env
	for _, env := range envVars {
		if env.Name == envVarName {
			return env.Value, pod.Name, nil
		}
	}
	return "unset", pod.Name, nil
}
