module github.com/rancher/fleet

go 1.14

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v14.0.0+incompatible
	helm.sh/helm/v3 => github.com/ibuildthecloud/helm/v3 v3.1.0-rc.1.0.20200811161532-4cb1fb31bcea
)

require (
	github.com/cheggaaa/pb v1.0.27
	github.com/hashicorp/go-getter v1.4.1
	github.com/pkg/errors v0.9.1
	github.com/rancher/gitjob v0.0.1-rc4
	github.com/rancher/lasso v0.0.0-20200820172840-0e4cc0ef5cb0
	github.com/rancher/wrangler v0.6.2-0.20200822010948-6d667521af49
	github.com/rancher/wrangler-cli v0.0.0-20200815040857-81c48cf8ab43
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.0.0
	golang.org/x/sync v0.0.0-20200317015054-43a5402ce75a
	helm.sh/helm/v3 v3.0.0
	k8s.io/api v0.18.8
	k8s.io/apimachinery v0.18.8
	k8s.io/cli-runtime v0.18.4
	k8s.io/client-go v0.18.8
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/kustomize/api v0.3.3-0.20200328155553-20184e9835c7
	sigs.k8s.io/kustomize/kstatus v0.0.2
	sigs.k8s.io/kustomize/kyaml v0.4.0
	sigs.k8s.io/yaml v1.2.0
)
