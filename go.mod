module github.com/rancher/fleet

go 1.13

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.0.0+incompatible
	helm.sh/helm/v3 => github.com/ibuildthecloud/helm/v3 v3.1.0-rc.1.0.20200328230414-e50c70bee3a6
)

require (
	github.com/cheggaaa/pb v1.0.27
	github.com/hashicorp/go-getter v1.4.1
	github.com/pkg/errors v0.9.1
	github.com/rancher/wrangler v0.6.1
	github.com/rancher/wrangler-api v0.6.0
	github.com/sirupsen/logrus v1.4.2
	github.com/spf13/cobra v0.0.7
	golang.org/x/sync v0.0.0-20200317015054-43a5402ce75a
	helm.sh/helm/v3 v3.0.0
	k8s.io/api v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/cli-runtime v0.18.0
	k8s.io/client-go v0.18.0
	k8s.io/klog v1.0.0
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/kustomize/api v0.3.3-0.20200328155553-20184e9835c7
	sigs.k8s.io/kustomize/kstatus v0.0.2-0.20200328155553-20184e9835c7
	sigs.k8s.io/kustomize/kyaml v0.1.1
	sigs.k8s.io/yaml v1.2.0
)
