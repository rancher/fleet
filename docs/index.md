# Introduction

!!! hint "Status"
    Fleet is currently alpha quality and actively being developed.

![](./arch.png)

Fleet is GitOps at scale. Fleet is designed to manage up to a million clusters. It's also lightweight
enough that is works great for a [single cluster](./single-cluster-install.md) too, but it really shines
when you get to a large scale. By large scale we mean either a lot of clusters, a lot of deployments, or a lot of
teams in a single organization.

Fleet can manage deployments from git of raw Kubernetes YAML, Helm charts, or Kustomize or any combination of the three.
Regardless of the source, all resources are dynamically turned into Helm charts, and Helm is used as the engine to
deploy everything in the cluster. This gives you a high degree of control, consistency, and auditability. Fleet focuses not only on
the ability to scale, but to give one a high degree of control and visibility to exactly what is installed on the cluster.
