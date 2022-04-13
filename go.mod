module github.com/rancher/fleet

go 1.16

replace (
	github.com/rancher/fleet/pkg/apis => ./pkg/apis
	helm.sh/helm/v3 => github.com/rancher/helm/v3 v3.6.3-fleet1
	k8s.io/api => k8s.io/api v0.21.3
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.21.3 // indirect
	k8s.io/apimachinery => k8s.io/apimachinery v0.21.3 // indirect
	k8s.io/apiserver => k8s.io/apiserver v0.21.3
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.21.3
	k8s.io/client-go => github.com/rancher/client-go v0.21.3-fleet1
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.21.3
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.21.3
	k8s.io/code-generator => k8s.io/code-generator v0.21.3
	k8s.io/component-base => k8s.io/component-base v0.21.3
	k8s.io/component-helpers => k8s.io/component-helpers v0.21.3
	k8s.io/controller-manager => k8s.io/controller-manager v0.21.3
	k8s.io/cri-api => k8s.io/cri-api v0.21.3
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.21.3
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.21.3
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.21.3
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.21.3
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.21.3
	k8s.io/kubectl => k8s.io/kubectl v0.21.3
	k8s.io/kubelet => k8s.io/kubelet v0.21.3
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.21.3
	k8s.io/metrics => k8s.io/metrics v0.21.3
	k8s.io/mount-utils => k8s.io/mount-utils v0.21.3
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.21.3
)

require (
	github.com/Masterminds/semver/v3 v3.1.1
	github.com/argoproj/argo-cd v1.8.7
	github.com/argoproj/gitops-engine v0.3.3
	github.com/cheggaaa/pb v1.0.27
	github.com/evanphx/json-patch v4.9.0+incompatible
	github.com/go-git/go-git/v5 v5.2.0
	github.com/go-openapi/spec v0.19.5
	github.com/google/go-containerregistry v0.1.1
	github.com/grpc-ecosystem/grpc-gateway v1.16.0 // indirect
	github.com/hashicorp/go-getter v1.5.11
	github.com/hashicorp/go-multierror v1.1.0 // indirect
	github.com/pkg/errors v0.9.1
	github.com/rancher/fleet/pkg/apis v0.0.0
	github.com/rancher/gitjob v0.1.5
	github.com/rancher/lasso v0.0.0-20210616224652-fc3ebd901c08
	github.com/rancher/wrangler v0.8.8
	github.com/rancher/wrangler-cli v0.0.0-20211112052728-f172e9bf59af
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.1.3
	github.com/stretchr/testify v1.7.0
	go.mozilla.org/sops/v3 v3.6.1
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	helm.sh/helm/v3 v3.5.1
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/cli-runtime v0.21.3
	k8s.io/client-go v11.0.1-0.20190816222228-6d55c1b1f1ca+incompatible
	k8s.io/kubernetes v1.21.3 // indirect
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/cli-utils v0.23.1
	sigs.k8s.io/kustomize/api v0.11.4
	sigs.k8s.io/kustomize/kyaml v0.13.6
	sigs.k8s.io/yaml v1.2.0
)
