module github.com/rancher/gitjob

go 1.15

replace (
	github.com/matryer/moq => github.com/rancher/moq v0.0.0-20190404221404-ee5226d43009
	k8s.io/client-go => k8s.io/client-go v0.20.2
)

require (
	github.com/gogits/go-gogs-client v0.0.0-20210131175652-1d7215cd8d85
	github.com/gorilla/mux v1.7.3
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/rancher/lasso v0.0.0-20210616224652-fc3ebd901c08
	github.com/rancher/steve v0.0.0-20210318171316-376934558c5b
	github.com/rancher/wrangler v0.8.4-0.20210727180331-41409b4e8965
	github.com/sirupsen/logrus v1.6.0
	github.com/urfave/cli v1.22.4
	github.com/whilp/git-urls v0.0.0-20191001220047-6db9661140c0
	gopkg.in/go-playground/webhooks.v5 v5.17.0
	k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/cli-utils v0.16.0
)
