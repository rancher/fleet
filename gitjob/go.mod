module github.com/rancher/gitjobs

go 1.12

replace github.com/matryer/moq => github.com/rancher/moq v0.0.0-20190404221404-ee5226d43009

require (
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/json-iterator/go v1.1.9 // indirect
	github.com/rancher/gitwatcher v0.4.5
	github.com/rancher/wrangler v0.6.1
	github.com/rancher/wrangler-api v0.6.0
	github.com/sirupsen/logrus v1.6.0
	github.com/stretchr/testify v1.6.1 // indirect
	github.com/urfave/cli v1.22.4
	golang.org/x/crypto v0.0.0-20200604202706-70a84ac30bf9 // indirect
	golang.org/x/net v0.0.0-20200602114024-627f9648deb9 // indirect
	golang.org/x/oauth2 v0.0.0-20200107190931-bf48bf16ab8d // indirect
	golang.org/x/time v0.0.0-20200416051211-89c76fbcd5d1 // indirect
	k8s.io/api v0.18.0
	k8s.io/apiextensions-apiserver v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/client-go v0.18.0
	k8s.io/utils v0.0.0-20200603063816-c1c6865ac451 // indirect
)
