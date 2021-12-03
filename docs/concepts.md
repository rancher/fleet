# Core Concepts

Fleet is fundamentally a set of Kubernetes custom resource definitions (CRDs) and controllers
to manage GitOps for a single Kubernetes cluster or a large-scale deployment of Kubernetes clusters. Note that Fleet is designed for mass horizontal scaling, but to date, scaling up to one million clusters has only been done in a test environment, not in production.

!!! note "Note"
    For more on the naming conventions of CRDs, click [here](./troubleshooting.md#naming-conventions-for-crds). 

Below are some of the concepts of Fleet that will be useful throughout this documentation:

* **Fleet Manager**: The centralized component that orchestrates the deployments of Kubernetes assets
    from git. In a multi-cluster setup, this will typically be a dedicated Kubernetes cluster. In a
    single cluster setup, the Fleet manager will be running on the same cluster you are managing with GitOps.
* **Fleet controller**: The controller(s) running on the Fleet manager orchestrating GitOps. In practice,
    the Fleet manager and Fleet controllers are used fairly interchangeably.
* **Single Cluster Style**: This is a style of installing Fleet in which the manager and downstream cluster are the
    same cluster.  This is a very simple pattern to quickly get up and running with GitOps.
* **Multi Cluster Style**: This is a style of running Fleet in which you have a central manager that manages a large
    number of downstream clusters.
* **Fleet agent**: Every managed downstream cluster will run an agent that communicates back to the Fleet manager.
    This agent is just another set of Kubernetes controllers running in the downstream cluster.
* **GitRepo**: Git repositories that are watched by Fleet are represented by the type `GitRepo`.

>**Example installation order via `GitRepo` custom resources when using Fleet for the configuration management of downstream clusters:**
>
> 1. Install [Calico](https://github.com/projectcalico/calico) CRDs and controllers.
> 2. Set one or multiple cluster-level global network policies.
> 3. Install [GateKeeper](https://github.com/open-policy-agent/gatekeeper). Note that **cluster labels** and **overlays** are critical features in Fleet as they determine which clusters will get each part of the bundle.
> 4. Set up and configure ingress and system daemons.

* **Bundle**: An internal unit used for the orchestration of resources from git.
    When a `GitRepo` is scanned it will produce one or more bundles. Bundles are a collection of
    resources that get deployed to a cluster. `Bundle` is the fundamental deployment unit used in Fleet. The
    contents of a `Bundle` may be Kubernetes manifests, Kustomize configuration, or Helm charts.
    Regardless of the source the contents are dynamically rendered into a Helm chart by the agent
    and installed into the downstream cluster as a helm release.

    - To see the **lifecycle of a bundle**, click [here](./examples.md#lifecycle-of-a-fleet-bundle).

* **BundleDeployment**: When a `Bundle` is deployed to a cluster an instance of a `Bundle` is called a `BundleDeployment`.
    A `BundleDeployment` represents the state of that `Bundle` on a specific cluster with its cluster specific
    customizations. The Fleet agent is only aware of `BundleDeployment` resources that are created for 
    the cluster the agent is managing.

    - For an example of how to deploy Kubernetes manifests across clusters using Fleet customization, click [here](./examples.md#deploy-kubernetes-manifests-across-clusters-with-customization).

* **Downstream Cluster**: Clusters to which Fleet deploys manifests are referred to as downstream clusters. In the single cluster use case, the Fleet manager Kubernetes cluster is both the manager and downstream cluster at the same time.
* **Cluster Registration Token**: Tokens used by agents to register a new cluster.