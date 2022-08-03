# Namespaces

All types in the Fleet manager are namespaced.  The namespaces of the manager types do not correspond to the namespaces
of the deployed resources in the downstream cluster. Understanding how namespaces are use in the Fleet manager is
important to understand the security model and how one can use Fleet in a multi-tenant fashion.

## GitRepos, Bundles, Clusters, ClusterGroups

The primary types are all scoped to a namespace. All selectors for `GitRepo` targets will be evaluated against
the `Clusters` and `ClusterGroups` in the same namespaces. This means that if you give `create` or `update` privileges
to a the `GitRepo` type in a namespace, that end user can modify the selector to match any cluster in that namespace.
This means in practice if you want to have two teams self manage their own `GitRepo` registrations but they should
not be able to target each others clusters, they should be in different namespaces.

## Namespace Creation Behavior in Bundles

When deploying a Fleet bundle, the specified namespace will automatically be created if it does not already exist.

## Special Namespaces

### fleet-local

The **fleet-local** namespace is a special namespace used for the single cluster use case or to bootstrap
the configuration of the Fleet manager.

When fleet is installed the `fleet-local` namespace is created along with one `Cluster` called `local` and one
`ClusterGroup` called `default`.  If no targets are specified on a `GitRepo`, it is by default targeted to the
`ClusterGroup` named `default`.  This means that all `GitRepos` created in `fleet-local` will
automatically target the `local` `Cluster`.  The `local` `Cluster` refers to the cluster the Fleet manager is running
on.

**Note:** If you would like to migrate your cluster from `fleet-local` to `default`, please see this [documentation](./troubleshooting.md#migrate-the-local-cluster-to-the-fleet-default-cluster).

### cattle-fleet-system

The Fleet controller and Fleet agent run in this namespace. All service accounts referenced by `GitRepos` are expected
to live in this namespace in the downstream cluster.

### cattle-fleet-clusters-system

This namespace holds secrets for the cluster registration process. It should contain no other resources in it,
especially secrets.

### Cluster namespaces

For every cluster that is registered a namespace is created by the Fleet manager for that cluster.
These namespaces have are named in the form `cluster-${namespace}-${cluster}-${random}`.  The purpose of this
namespace is that all `BundleDeployments` for that cluster are put into this namespace and
then the downstream cluster is given access to watch and update `BundleDeployments` in that namespace only.

## Cross namespace deployments

It is possible to create a GitRepo that will deploy across namespaces. The primary purpose of this is so that a
central privileged team can manage common configuration for many clusters that are managed by different teams. The way
this is accomplished is by creating a `BundleNamespaceMapping` resource in a cluster.

If you are creating a `BundleNamespaceMapping` resource it is best to do it in a namespace that only contains `GitRepos`
and no `Clusters`.  It seems to get confusing if you have Clusters in the same repo as the cross namespace `GitRepos` will still
always be evaluated against the current namespace.  So if you have clusters in the same namespace you may wish to make them
canary clusters.

A `BundleNamespaceMapping` has only two fields.  Which are as below

```yaml
kind: BundleNamespaceMapping
apiVersion: {{fleet.apiVersion}}
metadata:
  name: not-important
  namespace: typically-unique

# Bundles to match by label.  The labels are defined in the fleet.yaml
# labels field or from the GitRepo metadata.labels field
bundleSelector:
  matchLabels:
   foo: bar

# Namespaces to match by label
namespaceSelector:
  matchLabels:
   foo: bar
```

If the `BundleNamespaceMappings` `bundleSelector` field matches a `Bundles` labels then that `Bundle` target criteria will
be evaluated against all clusters in all namespaces that match `namespaceSelector`. One can specify labels for the created
bundles from git by putting labels in the `fleet.yaml` file or on the `metadata.labels` field on the `GitRepo`.

