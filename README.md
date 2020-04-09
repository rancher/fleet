Fleet
=============

### Status: early-ALPHA (actively looking for feedback)

![](docs/arch.png)

```
$ kubectl get fleet

NAME                                    CLUSTERS-READY   CLUSTERS-DESIRED   STATUS
bundle.fleet.cattle.io/helm-download    0                3                  NotApplied: 3 (default-bobby-group/cluster-93d18642-217a-486b-9a5d-be06762443b2... )
bundle.fleet.cattle.io/fleet-agent      3                3
bundle.fleet.cattle.io/helm-kustomize   0                3                  NotApplied: 3 (default-bobby-group/cluster-93d18642-217a-486b-9a5d-be06762443b2... )
bundle.fleet.cattle.io/helm             0                3                  NotApplied: 3 (default-bobby-group/cluster-93d18642-217a-486b-9a5d-be06762443b2... )
bundle.fleet.cattle.io/kustomize        0                3                  NotApplied: 3 (default-bobby-group/cluster-93d18642-217a-486b-9a5d-be06762443b2... )
bundle.fleet.cattle.io/yaml             0                3                  NotApplied: 3 (default-bobby-group/cluster-93d18642-217a-486b-9a5d-be06762443b2... )

NAME                                      CLUSTER-COUNT   NONREADY-CLUSTERS                                                                             BUNDLES-READY   BUNDLES-DESIRED   STATUS
clustergroup.fleet.cattle.io/othergroup   1               [cluster-f6a0e6da-ff49-4aab-9a21-fbe4687dd25b]                                                1               6                 NotApplied: 5 (helm... )
clustergroup.fleet.cattle.io/bobby        2               [cluster-93d18642-217a-486b-9a5d-be06762443b2 cluster-d7b5d925-fc56-45ca-92d5-de98f6728dd5]   2               12                NotApplied: 10 (helm... )

```

## Introduction

Fleet is a Kubernetes cluster fleet manager specifically designed to address the challenges of running
thousands to millions of clusters across the world.  While it's designed for massive scale the concepts still
apply for even small deployments of less than 10 clusters.  Fleet is lightweight enough to run on the smallest of
deployments too and even has merit in a single node cluster managing only itself. The primary use case of Fleet is
to ensure that deployments are consistents across clusters. One can deploy applications or easily enforce standards
such as "every cluster must have X security tool installed."

Fleet has two simple high level concepts: cluster groups and bundles.  Bundles are collections of resources that
are deployed to clusters. Bundles are defined in the fleet manager and are then deployed to target cluster using
 selectors and per target customization.  While bundles can be deployed to any cluster using powerful selectors,
 each cluster is a member of one cluster group. By looking at the status of bundles and cluster groups one can
 get a quick overview of that status of large deployments. After a bundle is deployed it is then constantly monitored
 to ensure that its Ready and resource have not been modified.

 A bundle can be plain Kubernetes YAML, Helm, or kustomize based. Helm and kustomize can be combined to create very
 powerful workflows too.  Regardless of the approach chosen to create bundles all resources are deployed to a cluster as
 helm charts. Using Fleet to manage clusters means all your clusters are easily auditable because every resource is
 carefully managed in a chart and a simple `helm -n fleet-system ls` will give you an accurate overview of what is
  installed.

Combining Fleet with a Git based workflow like Github Actions one can automate massive scale with ease.

## Documentation

1. [Understanding Bundles](./docs/bundles.md) - Very important read
1. [Example Bundles](./docs/examples.md)
1. [CLI](./docs/cli.md)
1. [Architecture and Installation](./docs/install.md)
1. [GitOps and CI/CD](./docs/gitops.md)

## Quick Start

1. Download `fleet` CLI from [releases](https://github.com/rancher/fleet/releases/latest).
   Or run
   ```bash
   curl -sfL https://raw.githubusercontent.com/rancher/fleet/master/install.sh | sh -
   ```
   
2. Install Fleet Manager on Kubernetes cluster.  The `fleet` CLI will use your current `kubectl` config
   to access the cluster.
    ```shell
    # Kubeconfig should point to MANAGER cluster
    fleet install manager | kubectl apply -f -
    ```
3. Generate cluster group token to register clusters
    ```shell script
    # Kubeconfig should point to MANAGER cluster
    fleet install agent-token > token
    ```
4. Apply token to clusters to register
    ```shell script
    # Kubeconfig should point to AGENT cluster
    kubectl apply -f token
    ```
5. Deploy some bundles
    ```shell script
    # Kubeconfig should point to MANAGER cluster
    fleet apply ./examples/helm-kustomize
    ```
6. Check status
   ```shell script
   kubectl get fleet
   ```
