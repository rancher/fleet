# Cluster Registration Tokens

!!! hint "Not needed for Manager initiated registration"
    For manager initiated registrations the token is managed by the Fleet manager and does
    not need to be manually created and obtained.

For an agent initiated registration the downstream cluster must have a cluster registration token.
Cluster registration tokens are used to establish a new identity for a cluster. Internally
cluster registration tokens are managed by creating Kubernetes service accounts that have the
permissions to create `ClusterRegistrationRequests` within a specific namespace.  Once the
cluster is registered a new `ServiceAccount` is created for that cluster that is used as
the unique identity of the cluster. The agent is designed to forget the cluster registration
token after registration. While the agent will not maintain a reference to the cluster registration
token after a successful registration please note that usually other system bootstrap scripts do.

Since the cluster registration token is forgotten, if you need to re-register a cluster you must
give the cluster a new registration token.

## Token TTL

Cluster registration tokens can be reused by any cluster in a namespace.  The tokens can be given a TTL
such that it will expire after a specific time.

## Create a new Token

The `ClusterRegistationToken` is a namespaced type and should be created in the same namespace
in which you will create `GitRepo` and `ClusterGroup` resources. For in depth details on how namespaces
are used in Fleet refer to the documentation on [namespaces](./namespaces.md).  Create a new
token with the below YAML.

```yaml
kind: ClusterRegistrationToken
apiVersion: "{{fleet.apiVersion}}"
metadata:
    name: new-token
    namespace: clusters
spec:
    # The number of seconds this token is valid after creation. A value <= 0 means infinite time.
    ttlSeconds: 604800
```

## Obtaining Token Value (Agent values.yaml)

The token value is the contents of a `values.yaml` file that is expected to be passed to `helm install`
to install the Fleet agent on a downstream cluster.  The token is stored in a Kubernetes secret referenced
by the `status.secretName` field on the newly created `ClusterRegistrationToken`.  In practice the secret
name is always the same as the `ClusterRegistrationToken` name. The contents will be in
the secret's data key `values`.  To obtain the `values.yaml` content for the above example YAML one can
run the following one-liner.

```shell
kubectl -n clusters get secret new-token -o 'jsonpath={.data.values}' | base64 -d > values.yaml
```

This `values.yaml` file can now be used repeatedly by clusters to register until the TTL expires.
