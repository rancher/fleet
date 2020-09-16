# Manager Initiated

Refer to the [overview page](./cluster-overview.md#agent-initiated-registration) for a background information on the manager initiated registration style.

## Kubeconfig Secret

The manager initiated registration flow is accomplished by creating a
`Cluster` resource in the Fleet Manager that refers to a Kubernetes
`Secret` containing a valid kubeconfig file in the data field called `value`.

The format of this secret is intended to match the [format](https://cluster-api.sigs.k8s.io/developer/architecture/controllers/cluster.html#secrets)
 of the kubeconfig
secret used in [cluster-api](https://github.com/kubernetes-sigs/cluster-api).
This means you can use `cluster-api` to create a cluster that is dynamically
registered with Fleet.

## Example

### Kubeconfig Secret
```yaml
kind: Secret
apiVersion: v1
metadata:
  name: my-cluster-kubeconfig
  namespace: clusters
data:
  value: YXBpVmVyc2lvbjogdjEKY2x1c3RlcnM6Ci0gY2x1c3RlcjoKICAgIHNlcnZlcjogaHR0cHM6Ly9leGFtcGxlLmNvbTo2NDQzCiAgbmFtZTogY2x1c3Rlcgpjb250ZXh0czoKLSBjb250ZXh0OgogICAgY2x1c3RlcjogY2x1c3RlcgogICAgdXNlcjogdXNlcgogIG5hbWU6IGRlZmF1bHQKY3VycmVudC1jb250ZXh0OiBkZWZhdWx0CmtpbmQ6IENvbmZpZwpwcmVmZXJlbmNlczoge30KdXNlcnM6Ci0gbmFtZTogdXNlcgogIHVzZXI6CiAgICB0b2tlbjogc29tZXRoaW5nCg==
```
### Cluster
```yaml
apiVersion: fleet.cattle.io/v1alpha1
kind: Cluster
metadata:
  name: my-cluster
  namespace: clusters
  labels:
    demo: "true"
    env: dev
spec:
  kubeConfigSecret: my-cluster-kubeconfig
```





