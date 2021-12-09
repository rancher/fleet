# Examples

### Lifecycle of a Fleet Bundle

To demonstrate the lifecycle of a Fleet bundle, we will use [multi-cluster/helm](https://github.com/rancher/fleet-examples/tree/master/multi-cluster/helm) as a case study.

1. User will create a [GitRepo](./gitrepo-add.md#create-gitrepo-instance) that points to the multi-cluster/helm repository.
2. The `gitjob-controller` will sync changes from the GitRepo and detect changes from the polling or [webhook event](./webhook.md). With every commit change, the `gitjob-controller` will create a job that clones the git repository, reads content from the repo such as `fleet.yaml` and other manifests, and creates the Fleet [bundle](./cluster-bundles-state.md#bundles).

>**Note:** The job pod with the image name `rancher/tekton-utils` will be under the same namespace as the GitRepo.

3. The `fleet-controller` then syncs changes from the bundle. According to the targets, the `fleet-controller` will create `BundleDeployment` resources, which are a combination of a bundle and a target cluster.
4. The `fleet-agent` will then pull the `BundleDeployment` from the Fleet controlplane. The agent deploys bundle manifests as a [Helm chart](https://helm.sh/docs/intro/install/) from the `BundleDeployment` into the downstream clusters.
5. The `fleet-agent` will continue to monitor the application bundle and report statuses back in the following order: bundledeployment > bundle > GitRepo > cluster.

### Deploy Kubernetes Manifests Across Clusters with Customization

[Fleet in Rancher](https://rancher.com/docs/rancher/v2.6/en/deploy-across-clusters/fleet/) allows users to manage clusters easily as if they were one cluster. Users can deploy bundles, which can be comprised of deployment manifests or any other Kubernetes resource, across clusters using grouping configuration.

To demonstrate how to deploy Kubernetes manifests across different clusters using Fleet, we will use [multi-cluster/helm/fleet.yaml](https://github.com/rancher/fleet-examples/blob/master/multi-cluster/helm/fleet.yaml) as a case study.

**Situation:** User has three clusters with three different labels: `env=dev`, `env=test`, and `env=prod`. User wants to deploy a frontend application with a backend database across these clusters. 

**Expected behavior:** 

- After deploying to the `dev` cluster, database replication is not enabled.
- After deploying to the `test` cluster, database replication is enabled.
- After deploying to the `prod` cluster, database replication is enabled and Load balancer services are exposed.

**Advantage of Fleet:**

Instead of deploying the app on each cluster, Fleet allows you to deploy across all clusters following these steps:

1. Deploy gitRepo `https://github.com/rancher/fleet-examples.git` and specify the path `multi-cluster/helm`.
2. Under `multi-cluster/helm`, a Helm chart will deploy the frontend app service and backend database service.
3. The following rule will be defined in `fleet.yaml`: 

```
targetCustomizations:
- name: dev
  helm:
    values:
      replication: false
  clusterSelector:
    matchLabels:
      env: dev

- name: test
  helm:
    values:
      replicas: 3
  clusterSelector:
    matchLabels:
      env: test

- name: prod
  helm:
    values:
      serviceType: LoadBalancer
      replicas: 3
  clusterSelector:
    matchLabels:
      env: prod
```

**Result:**

Fleet will deploy the Helm chart with your customized `values.yaml` to the different clusters.

>**Note:** Configuration management is not limited to deployments but can be expanded to general configuration management. Fleet is able to apply configuration management through customization among any set of clusters automatically.

### Additional Examples

Examples using raw Kubernetes YAML, Helm charts, Kustomize, and combinations
of the three are in the [Fleet Examples repo](https://github.com/rancher/fleet-examples/).
