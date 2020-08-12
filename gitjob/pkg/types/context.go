package types

import (
	"context"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/crd"
	"github.com/rancher/wrangler/pkg/generated/controllers/batch"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/pkg/schemas/openapi"
	"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type contextKey struct{}

type Context struct {
	Namespace string

	Batch  *batch.Factory
	Gitjob *gitjob.Factory
	Core   *core.Factory
	K8s    kubernetes.Interface

	Apply apply.Apply
}

func Store(ctx context.Context, c *Context) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

func From(ctx context.Context) *Context {
	return ctx.Value(contextKey{}).(*Context)
}

func NewContext(namespace string, config *rest.Config) *Context {
	context := &Context{
		Namespace: namespace,
		Batch:     batch.NewFactoryFromConfigOrDie(config),
		Core:      core.NewFactoryFromConfigOrDie(config),
		Gitjob:    gitjob.NewFactoryFromConfigOrDie(config),
		K8s:       kubernetes.NewForConfigOrDie(config),
	}

	context.Apply = apply.New(context.K8s.Discovery(), apply.NewClientFactory(config))
	return context
}

func (c *Context) Start(ctx context.Context) error {
	return start.All(ctx, 5,
		c.Gitjob,
		c.Core,
	)
}

func BuildContext(ctx context.Context, namespace string, config *rest.Config) (context.Context, *Context) {
	factory, err := crd.NewFactoryFromClient(config)
	if err != nil {
		logrus.Fatal(err)
	}

	factory.BatchCreateCRDs(ctx, getCRDs()...)

	if err := factory.BatchWait(); err != nil {
		logrus.Fatal(err)
	}

	c := NewContext(namespace, config)
	return context.WithValue(ctx, contextKey{}, c), c
}

func getCRDs() []crd.CRD {
	return []crd.CRD{
		crd.NamespacedType("GitJob.gitjob.cattle.io/v1").WithStatus().WithSchema(mustSchema(v1.GitJob{})),
	}
}

func mustSchema(obj interface{}) *v1beta1.JSONSchemaProps {
	result, err := openapi.ToOpenAPIFromStruct(obj)
	if err != nil {
		panic(err)
	}
	return result
}
