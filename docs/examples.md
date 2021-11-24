# Examples

### Lifecycle of a Fleet Bundle

To demonstrate the lifecycle of a Fleet bundle, we will use multi-cluster/helm as a case study.

1. User creates a [GitRepo](./gitrepo-add.md#create-gitrepo-instance) that points to the [multi-cluster/helm](https://github.com/rancher/fleet-examples/tree/master/multi-cluster/helm) repository.
2. The Gitjob controller will sync changes from the GitRepo and detect changes from the poll/webhook. With every commit change, the Gitjob controller will create a job that clones the git repository, reads content from the repo such as fleet.yaml and other manifests, and creates the Fleet [bundle](./cluster-bundles-state.md#bundles).
3. The Fleet-controller then syncs changes from the bundle. According to the targets, the Fleet-controller will create `BundleDeployment` resources, which are a combination of a bundle and a target cluster.
4. The Fleet-agent will then pull the `BundleDeployment` from the Fleet controlplane. The agent deploys a real application and configuration as a [Helm chart](https://helm.sh/docs/intro/install/) from the `BundleDeployment` into the downstream clusters.
5. The Fleet-agent will continue to monitor the application bundle and report statuses back in the following order: bundledeployment > bundle > GitRepo > cluster.

### Additional Examples

Examples using raw Kubernetes YAML, Helm charts, Kustomize, and combinations
of the three are in the [Fleet Examples repo](https://github.com/rancher/fleet-examples/).
