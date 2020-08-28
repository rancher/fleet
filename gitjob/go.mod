module github.com/rancher/gitjob

go 1.12

replace github.com/matryer/moq => github.com/rancher/moq v0.0.0-20190404221404-ee5226d43009

require (
	github.com/golang/groupcache v0.0.0-20190702054246-869f871628b6 // indirect
	github.com/google/go-cmp v0.3.1 // indirect
	github.com/google/go-github/v28 v28.0.0
	github.com/google/uuid v1.1.1
	github.com/gorilla/mux v1.7.1
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/json-iterator/go v1.1.9 // indirect
	github.com/rancher/lasso v0.0.0-20200820172840-0e4cc0ef5cb0
	github.com/rancher/wrangler v0.6.2-0.20200828062337-113afceceb91
	github.com/sirupsen/logrus v1.6.0
	github.com/stretchr/testify v1.6.1 // indirect
	github.com/urfave/cli v1.22.4
	github.com/whilp/git-urls v0.0.0-20191001220047-6db9661140c0
	golang.org/x/crypto v0.0.0-20200604202706-70a84ac30bf9 // indirect
	golang.org/x/net v0.0.0-20200602114024-627f9648deb9 // indirect
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d
	golang.org/x/time v0.0.0-20200416051211-89c76fbcd5d1 // indirect
	k8s.io/api v0.18.8
	k8s.io/apiextensions-apiserver v0.18.0
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v0.18.8
	k8s.io/utils v0.0.0-20200603063816-c1c6865ac451 // indirect
	sigs.k8s.io/cli-utils v0.16.0
)
