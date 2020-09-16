# Cluster Groups

Clusters in a namespace can be put into a cluster group. A cluster group is essentially a named selector.
The only parameter for a cluster group is essentially the selector.
When you get to a certain scale cluster groups become a more reasonable way to manage your clusters.
Cluster groups serve the purpose of giving aggregated
status of the deployments and then also a simpler way to manage targets.

A cluster group is created by creating a `ClusterGroup` resource like below

```yaml
kind: ClusterGroup
apiVersion: {{fleet.apiVersion}}
metadata:
  name: production-group
  namespace: clusters
spec:
  # This is the standard metav1.LabelSelector format to match clusters by labels
  selector:
    matchLabels:
      env: prod
```
