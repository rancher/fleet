module github.com/rancher/fleet

go 1.14

replace (
	github.com/rancher/fleet/pkg/apis => ./pkg/apis
	helm.sh/helm/v3 => github.com/rancher/helm/v3 v3.3.3-fleet1
	k8s.io/client-go => github.com/rancher/client-go v0.20.0-fleet1
)

require (
	github.com/cheggaaa/pb v1.0.27
	github.com/gregjones/httpcache v0.0.0-20190212212710-3befbb6ad0cc // indirect
	github.com/hashicorp/go-getter v1.4.1
	github.com/pkg/errors v0.9.1
	github.com/rancher/fleet/pkg/apis v0.0.0
	github.com/rancher/gitjob v0.1.5
	github.com/rancher/lasso v0.0.0-20210218221607-54c79222a9ad
	github.com/rancher/wrangler v0.7.3-0.20210219171705-1969c99916dd
	github.com/rancher/wrangler-cli v0.0.0-20200815040857-81c48cf8ab43
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.1.1
	go.mozilla.org/sops/v3 v3.6.1
	golang.org/x/sync v0.0.0-20200317015054-43a5402ce75a
	helm.sh/helm/v3 v3.5.1
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/cli-runtime v0.20.0
	k8s.io/client-go v0.20.0
	k8s.io/kubectl v0.20.0 // indirect
	rsc.io/letsencrypt v0.0.3 // indirect
	sigs.k8s.io/cli-utils v0.16.0
	sigs.k8s.io/kustomize/api v0.6.0
	sigs.k8s.io/kustomize/kyaml v0.7.1
	sigs.k8s.io/yaml v1.2.0
)
