# Overview

![](./arch.png)

### What is Fleet?

- **Cluster engine**: Fleet is a container management and deployment engine designed to offer users more control on the local cluster and constant monitoring through **GitOps**. Fleet focuses not only on the ability to scale, but it also gives users a high degree of control and visibility to monitor exactly what is installed on the cluster.

- **GitOps at scale**: Fleet can manage up to a million clusters, but it's also lightweight enough that it works well for a [single cluster](./single-cluster-install.md). Fleet's capabilities are fully realized when it's used for large-scale projects. "Large scale" can mean a lot of clusters, a lot of deployments, or a lot of teams in a single organization.

- **Deployment management**: Fleet can manage deployments from git of raw Kubernetes YAML, Helm charts, Kustomize, or any combination of the three. Regardless of the source, all resources are dynamically turned into Helm charts, and Helm is used as the engine to deploy all resources in the cluster. As a result, users have a high degree of control, consistency, and auditability.

### Configuration Management

Fleet is fundamentally a set of Kubernetes [custom resource definitions (CRDs)](https://fleet.rancher.io/concepts/) and controllers that manage GitOps for a single Kubernetes cluster or a large scale deployment of Kubernetes clusters (up to one million). It is a distributed initialization system that makes it easy to customize applications and manage HA clusters from a single point.
