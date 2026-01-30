// +vendored https://github.com/argoproj/gitops-engine/blob/master/pkg/utils/kube/scheme/scheme.go
// Modified to use k8s.io/client-go/kubernetes/scheme instead of k8s.io/kubernetes due to compilation issues in k8s.io/kubernetes v1.35.0
package scheme

import (
	kubescheme "k8s.io/client-go/kubernetes/scheme"
)

var Scheme = kubescheme.Scheme
