# Troubleshooting

This section contains commands and tips to troubleshoot Fleet.

## **How Do I...**


### Fetch the log from `fleet-controller`?

In the local management cluster where the `fleet-controller` is deployed, run the following command with your specific `fleet-controller` pod name filled in:

```
$ kubectl logs -l app=fleet-controller -n cattle-fleet-system
```

### Fetch the log from the `fleet-agent`?

Go to each downstream cluster and run the following command for the local cluster with your specific `fleet-agent` pod name filled in:

```
# Downstream cluster
$ kubectl logs -l app=fleet-agent -n cattle-fleet-system
# Local cluster
$ kubectl logs -l app=fleet-agent -n cattle-local-fleet-system
```

### Fetch detailed error logs from `GitRepos` and `Bundles`?

Normally, errors should appear in the Rancher UI. However, if there is not enough information displayed about the error there, you can research further by trying one or more of the following as needed:

- For more information about the bundle, click on `bundle`, and the YAML mode will be enabled. 
- For more information about the GitRepo, click on `GitRepo`, then click on `View Yaml` in the upper right of the screen. After viewing the YAML, check `status.conditions`; a detailed error message should be displayed here.
- Check the `fleet-controller` for synching errors.
- Check the `fleet-agent` log in the downstream cluster if you encounter issues when deploying the bundle.

### Check a chart rendering error in `Kustomize`?

Check the [`fleet-controller` logs](./troubleshooting.md#fetch-the-log-from-fleet-controller) and the [`fleet-agent` logs](./troubleshooting.md#fetch-the-log-from-the-fleet-agent).

### Check errors about watching or checking out the `GitRepo`, or about the downloaded Helm repo in `fleet.yaml`?

Check the `gitjob-controller` logs using the following command with your specific `gitjob` pod name filled in:

```
$ kubectl logs -f $gitjob-pod-name -n cattle-fleet-system
```

Note that there are two containers inside the pod: the `step-git-source` container that clones the git repo, and the `fleet` container that applies bundles based on the git repo. 

The pods will usually have images named `rancher/tekton-utils` with the `gitRepo` name as a prefix. Check the logs for these Kubernetes job pods in the local management cluster as follows, filling in your specific `gitRepoName` pod name and namespace:

```
$ kubectl logs -f $gitRepoName-pod-name -n namespace
```

### Check the status of the `fleet-controller`?

You can check the status of the `fleet-controller` pods by running the commands below:

```bash
kubectl -n cattle-fleet-system logs -l app=fleet-controller
kubectl -n cattle-fleet-system get pods -l app=fleet-controller
```

```bash
NAME                                READY   STATUS    RESTARTS   AGE
fleet-controller-64f49d756b-n57wq   1/1     Running   0          3m21s
```

### Migrate the local cluster to the Fleet default cluster?

For users who want to deploy to the local cluster as well, they may move the cluster from `fleet-local` to `fleet-default` in the Rancher UI as follows:

- To get to Fleet in Rancher, click ☰ > Continuous Delivery.
- Under the **Clusters** menu, select the **local** cluster by checking the box to the left.
- Select **Assign to** from the tabs above the cluster.
- Select **`fleet-default`** from the **Assign Cluster To** dropdown.

**Result**: The cluster will be migrated to `fleet-default`.

### Enable debug logging for `fleet-controller` and `fleet-agent`?

Available in Rancher v2.6.3 (Fleet v0.3.8), the ability to enable debug logging has been added.

- Go to the **Dashboard**, then click on the **local cluster** in the left navigation menu 
- Select **Apps & Marketplace**, then **Installed Apps** from the dropdown 
- From there, you will upgrade the Fleet chart with the value `debug=true`. You can also set `debugLevel=5` if desired.

## **Additional Solutions for Other Fleet Issues**

### Naming conventions for CRDs

1. For CRD terms like `clusters` and `gitrepos`, you must reference the full CRD name. For example, the cluster CRD's complete name is `cluster.fleet.cattle.io`, and the gitrepo CRD's complete name is `gitrepo.fleet.cattle.io`.

1. `Bundles`, which are created from the `GitRepo`, follow the pattern `$gitrepoName-$path` in the same workspace/namespace where the `GitRepo` was created. Note that `$path` is the path directory in the git repository that contains the `bundle` (`fleet.yaml`).

1. `BundleDeployments`, which are created from the `bundle`, follow the pattern `$bundleName-$clusterName` in the namespace `clusters-$workspace-$cluster-$generateHash`. Note that `$clusterName` is the cluster to which the bundle will be deployed.

### HTTP secrets in Github

When testing Fleet with private git repositories, you will notice that HTTP secrets are no longer supported in Github. To work around this issue, follow these steps:

1. Create a [personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/creating-a-personal-access-token) in Github.
1. In Rancher, create an HTTP [secret](https://rancher.com/docs/rancher/v2.6/en/k8s-in-rancher/secrets/) with your Github username.
1. Use your token as the secret.

### Fleet fails with bad response code: 403

If your GitJob returns the error below, the problem may be that Fleet cannot access the Helm repo you specified in your [`fleet.yaml`](./gitrepo-structure.md):

```
time="2021-11-04T09:21:24Z" level=fatal msg="bad response code: 403"
```

Perform the following steps to assess:

- Check that your repo is accessible from your dev machine, and that you can download the Helm chart successfully
- Check that your credentials for the git repo are valid

### Helm chart repo: certificate signed by unknown authority

If your GitJob returns the error below, you may have added the wrong certificate chain:

```
time="2021-11-11T05:55:08Z" level=fatal msg="Get \"https://helm.intra/virtual-helm/index.yaml\": x509: certificate signed by unknown authority" 
```

Please verify your certificate with the following command:

```bash
context=playground-local
kubectl get secret -n fleet-default helm-repo -o jsonpath="{['data']['cacerts']}" --context $context | base64 -d | openssl x509 -text -noout
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number:
            7a:1e:df:79:5f:b0:e0:be:49:de:11:5e:d9:9c:a9:71
        Signature Algorithm: sha512WithRSAEncryption
        Issuer: C = CH, O = MY COMPANY, CN = NOP Root CA G3
...

```
### Fleet deployment stuck in modified state

When you deploy bundles to Fleet, some of the components are modified, and this causes the "modified" flag in the Fleet environment.

To ignore the modified flag for the differences between the Helm install generated by `fleet.yaml` and the resource in your cluster, add a `diff.comparePatches` to the `fleet.yaml` for your Deployment, as shown in this example:


```yaml
defaultNamespace: <namespace name> 
helm:  
  releaseName: <release name>  
  repo: <repo name> 
  chart: <chart name>
diff:  
  comparePatches:  
  - apiVersion: apps/v1
    kind: Deployment
    operations:
    - {"op":"remove", "path":"/spec/template/spec/hostNetwork"}
    - {"op":"remove", "path":"/spec/template/spec/nodeSelector"}
    jsonPointers: # jsonPointers allows to ignore diffs at certain json path
    - "/spec/template/spec/priorityClassName"
    - "/spec/template/spec/tolerations" 
```

To determine which operations should be removed, observe the logs from `fleet-agent` on the target cluster. You should see entries similar to the following:

```text
level=error msg="bundle monitoring-monitoring: deployment.apps monitoring/monitoring-monitoring-kube-state-metrics modified {\"spec\":{\"template\":{\"spec\":{\"hostNetwork\":false}}}}"
```

Based on the above log, you can add the following entry to remove the operation:

```json
{"op":"remove", "path":"/spec/template/spec/hostNetwork"}
```

### `GitRepo` or `Bundle` stuck in modified state

**Modified** means that there is a mismatch between the actual state and the desired state, the source of truth, which lives in the git repository.

1. Check the [bundle diffs documentation](./bundle-diffs.md) for more information. 

1. You can also force update the `gitrepo` to perform a manual resync. Select **GitRepo** on the left navigation bar, then select **Force Update**.

### Bundle has a Horizontal Pod Autoscaler (HPA) in modified state

For bundles with an HPA, the expected state is `Modified`, as the bundle contains fields that differ from the state of the Bundle at deployment - usually `ReplicaSet`.

You must define a patch in the `fleet.yaml` to ignore this field according to [`GitRepo` or `Bundle` stuck in modified state](#gitrepo-or-bundle-stuck-in-modified-state).

Here is an example of such a patch for the deployment `nginx` in namespace `default`:

```yaml
diff:
  comparePatches:
  - apiVersion: apps/v1
    kind: Deployment
    name: nginx
    namespace: default
    operations:
    - {"op": "remove", "path": "/spec/replicas"}
```

### What if the cluster is unavailable, or is in a `WaitCheckIn` state?

You will need to re-import and restart the registration process: Select **Cluster** on the left navigation bar, then select **Force Update**

!!! note "WaitCheckIn status for Rancher v2.5"
    The cluster will show in `WaitCheckIn` status because the `fleet-controller` is attempting to communicate with Fleet using the Rancher service IP. However, Fleet must communicate directly with Rancher via the Kubernetes service DNS using service discovery, not through the proxy. For more, see the [Rancher docs](https://rancher.com/docs/rancher/v2.5/en/installation/other-installation-methods/behind-proxy/install-rancher/#install-rancher). 

### GitRepo complains with `gzip: invalid header`

When you see an error like the one below ...

```sh
Error opening a gzip reader for /tmp/getter154967024/archive: gzip: invalid header
```

... the content of the helm chart is incorrect. Manually download the chart to your local machine and check the content.