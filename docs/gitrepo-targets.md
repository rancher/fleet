# Mapping to Downstream Clusters

!!! hint "Multi-cluster Only"
    This approach only applies if you are running Fleet in a multi-cluster style

When deploying `GitRepos` to downstream clusters the clusters must be mapped to a target.

## Defining targets

The deployment targets of `GitRepo` is done using the `spec.targets` field to
match clusters or cluster groups. The YAML specification is as below.

```yaml
kind: GitRepo
apiVersion: {{fleet.apiVersion}}
metadata:
  name: myrepo
  namespace: clusters
spec:
  repo: https://github.com/rancher/fleet-examples
  paths:
  - simple

  # Targets are evaluated in order and the first one to match is used. If
  # no targets match then the evaluated cluster will not be deployed to.
  targets:
  # The name of target. This value is largely for display and logging.
  # If not specified a default name of the format "target000" will be used
  - name: prod
    # A selector used to match clusters.  The structure is the standard
    # metav1.LabelSelector format. If clusterGroupSelector or clusterGroup is specified,
    # clusterSelector will be used only to further refine the selection after
    # clusterGroupSelector and clusterGroup is evaluated.
    clusterSelector:
      matchLabels:
        env: prod
    # A selector used to match cluster groups.
    clusterGroupSelector:
      matchLabels:
        region: us-east
    # A specific clusterGroup by name that will be selected
    clusterGroup: group1
```

## Target Matching

All clusters and cluster groups in the same namespace as the `GitRepo` will be evaluated against all targets.
If any of the targets match the cluster then the `GitRepo` will be deployed to the downstream cluster. If
no match is made, then the `GitRepo` will not be deployed to that cluster.

There are three approaches to matching clusters.
One can use cluster selectors, cluster group selectors, or an explicit cluster group name.  All criteria is additive so
the final match is evaluated as "clusterSelector && clusterGroupSelector && clusterGroup".  If any of the three have the
default value it is dropped from the criteria.  The default value is either null or "".  It is important to realize
that the value `{}` for a selector means "match everything."

```yaml
# Match everything
clusterSelector: {}
# Selector ignored
clusterSelector: null
```

## Default target

If no target is set for the `GitRepo` then the default targets value is applied.  The default targets value is as below.

```yaml
targets:
- name: default
  clusterGroup: default
```

This means if you wish to setup a default location non-configured GitRepos will go to, then just create a cluster group called default
and add clusters to it.
