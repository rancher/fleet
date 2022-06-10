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
    # A duration string for how long this token is valid for. A value <= 0 or null means infinite time.
    ttl: 240h
```

After the `ClusterRegistrationToken` is created, Fleet will create a corresponding `Secret` with the same name.
As the `Secret` creation is performed asynchronously, you will need to wait until it's available before using it.

One way to do so is via the following one-liner:
```shell
while ! kubectl --namespace=clusters  get secret new-token; do sleep 5; done
```

## Obtaining Token Value (Agent values.yaml)

The token value contains YAML content for a `values.yaml` file that is expected to be passed to `helm install`
to install the Fleet agent on a downstream cluster.

Such value is contained in the `values` field of the `Secret` mentioned above. To obtain the YAML content for the
above example one can run the following one-liner:
```shell
kubectl --namespace clusters get secret new-token -o 'jsonpath={.data.values}' | base64 --decode > values.yaml
```

Once the `values.yaml` is ready it can be used repeatedly by clusters to register until the TTL expires.
