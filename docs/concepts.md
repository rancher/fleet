# Core Concepts

Fleet is fundamentally a set of Kubernetes custom resource definitions (CRDs) and controllers
to manage GitOps for a single Kubernetes cluster or a large scale deployments of Kubernetes clusters
(up to one million). Below are some of the concepts of Fleet that will be useful through out this documentation.

* **Fleet Manager**: The centralized component that orchestrates the deployments of Kubernetes assets
    from git. In a multi-cluster setup this will typically be a dedicated Kubernetes cluster. In a
    single cluster setup the Fleet manager will be running on the same cluster you are managing with GitOps.
* **Fleet controller**: The controller(s) running on the Fleet manager orchestrating GitOps. In practice
    Fleet manager and Fleet controllers is used fairly interchangeably.
* **Single Cluster Style**: This is a style of installing Fleet in which the manager and downstream cluster are the
    same cluster.  This is a very simple pattern to quickly get up and running with GitOps.
* **Multi Cluster Style**: This is a style of running Fleet in which you have a central manager that manages a large
    number of downstream clusters.
* **Fleet agent**: Every managed downstream cluster will run an agent that communicates back to the Fleet manager.
    This agent is just another set of Kubernetes controllers running in the downstream cluster.
* **GitRepo**: Git repositories that are watched by Fleet are represented by the type `GitRepo`.
* **Bundle**: When a `GitRepo` is scanned it will produce one or more bundles. Bundles are a collection of
    resources that get deployed to a cluster. `Bundle` is the fundamental deployment unit used in Fleet. The
    contents of a `Bundle` may be Kubernetes manifests, Kustomize configuration, or Helm charts.
* **BundleDeployment**: When a `Bundle` is deployed to a cluster an instance of a `Bundle` is called a `BundleDeployment`.
    A `BundleDeployment` represents the state of that `Bundle` on a specific cluster with it's cluster specific
    customizations.
* **Downstream Cluster**: Clusters to which Fleet deploys manifests are referred to as downstream clusters. In the single
    cluster use case the Fleet manager Kubernetes cluster is both the manager and downstream cluster at the same time.
* **Cluster Registration Token**: Tokens used by agents to register a new cluster.