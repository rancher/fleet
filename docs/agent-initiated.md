# Agent Initiated

Refer to the [overview page](./cluster-overview.md#agent-initiated-registration) for a background information on the agent initiated registration style.

## Cluster Registration Token and Client ID

A downstream cluster is registered using the **cluster registration token** and optionally a **client ID** or **cluster labels**.

The **cluster registration token** is a credential that will authorize the downstream cluster agent to be
able to initiate the registration process. This is required. Refer to the [cluster registration token page](./cluster-tokens.md) for more information
on how to create tokens and obtain the values. The cluster registration token is manifested as a `values.yaml` file that will
be passed to the `helm install` process.

There are two styles of registering an agent. You can have the cluster for this agent dynamically created, in which
case you will probably want to specify **cluster labels** upon registration.  Or you can have the agent register to a predefined
cluster in the Fleet manager, in which case you will need a **client ID**.  The former approach is typically the easiest.

## Install agent for a new Cluster

The Fleet agent is installed as a Helm chart. Following are explanations how to determine and set its parameters.

First, follow the [cluster registration token page](./cluster-tokens.md) to obtain the `values.yaml` which contains
the registration token to authenticate against the Fleet cluster.

Second, optionally you can define labels that will assigned to the newly created cluster upon registration. After
registration is completed an agent cannot change the labels of the cluster. To add cluster labels add
`--set-string labels.KEY=VALUE` to the below Helm command. To add the labels `foo=bar` and `bar=baz` then you would
add `--set-string labels.foo=bar --set-string labels.bar=baz` to the command line.

```shell
# Leave blank if you do not want any labels
CLUSTER_LABELS="--set-string labels.example=true --set-string labels.env=dev"
```

Third, set variables with the Fleet cluster's API Server URL and CA, for the downstream cluster to use for connecting.

```shell
API_SERVER_URL=https://...
API_SERVER_CA=...
```

Value in `API_SERVER_CA` can be obtained from a `.kube/config` file with valid data to connect to the upstream cluster
(under the `certificate-authority-data` key). Alternatively it can be obtained from within the upstream cluster itself,
by looking up the default ServiceAccount secret name (typically prefixed with `default-token-`, in the default namespace),
under the `ca.crt` key.


!!! hint "Use proper namespace and release name"
    For the agent chart the namespace must be `cattle-fleet-system` and the release name `fleet-agent`

!!! hint "Ensure you are installing to the right cluster"
    Helm will use the default context in `${HOME}/.kube/config` to deploy the agent. Use `--kubeconfig` and `--kube-context`
    to change which cluster Helm is installing to.
    
Finally, install the agent using Helm.

```shell
helm -n cattle-fleet-system install --create-namespace --wait \
    ${CLUSTER_LABELS} \
    --values values.yaml \
    --set apiServerCA=${API_SERVER_CA} \
    --set apiServerURL=${API_SERVER_URL} \
    fleet-agent https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-agent-{{fleet.helmversion}}.tgz
```

The agent should now be deployed.  You can check that status of the fleet pods by running the below commands.

```shell
# Ensure kubectl is pointing to the right cluster
kubectl -n cattle-fleet-system logs -l app=fleet-agent
kubectl -n cattle-fleet-system get pods -l app=fleet-agent
```

Additionally you should see a new cluster registered in the Fleet manager.  Below is an example of checking that a new cluster
was registered in the `clusters` [namespace](./namespaces.md).  Please ensure your `${HOME}/.kube/config` is pointed to the Fleet
manager to run this command.

```shell
kubectl -n clusters get clusters.fleet.cattle.io
```
```
NAME                   BUNDLES-READY   NODES-READY   SAMPLE-NODE             LAST-SEEN              STATUS
cluster-ab13e54400f1   1/1             1/1           k3d-cluster2-server-0   2020-08-31T19:23:10Z   
```

## Install agent for a predefined Cluster

Client IDs are for the purpose of predefining clusters in the Fleet manager with existing labels and repos targeted to them.
A client ID is not required and is just one approach to managing clusters.
The **client ID** is a unique string that will identify the cluster.
This string is user generated and opaque to the Fleet manager and agent.  It is assumed to be sufficiently unique. For security reasons one should not be able to easily guess this value
as then one cluster could impersonate another.  The client ID is optional and if not specified the UID field of the `kube-system` namespace
resource will be used as the client ID. Upon registration if the client ID is found on a `Cluster` resource in the Fleet manager it will associate
the agent with that `Cluster`.  If no `Cluster` resource is found with that client ID a new `Cluster` resource will be created with the specific
client ID.

The Fleet agent is installed as a Helm chart. The only parameters to the helm chart installation should be the cluster registration token, which
is represented by the `values.yaml` file and the client ID.  The client ID is optional.


First, create a `Cluster` in the Fleet Manager with the random client ID you have chosen.

```yaml
kind: Cluster
apiVersion: {{fleet.apiVersion}}
metadata:
  name: my-cluster
  namespace: clusters
spec:
  clientID: "really-random"
```

Second, follow the [cluster registration token page](./cluster-tokens.md) to obtain the `values.yaml` file to be used.

Third, setup your environment to use the client ID.

```shell
CLUSTER_CLIENT_ID="really-random"
```

!!! hint "Use proper namespace and release name"
    For the agent chart the namespace must be `cattle-fleet-system` and the release name `fleet-agent`

!!! hint "Ensure you are installing to the right cluster"
    Helm will use the default context in `${HOME}/.kube/config` to deploy the agent. Use `--kubeconfig` and `--kube-context`
    to change which cluster Helm is installing to.
    
Finally, install the agent using Helm.

```shell
helm -n cattle-fleet-system install --create-namespace --wait \
    --set clientID="${CLUSTER_CLIENT_ID}" \
    --values values.yaml \
    fleet-agent https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-agent-{{fleet.version}}.tgz
```

The agent should now be deployed.  You can check that status of the fleet pods by running the below commands.

```shell
# Ensure kubectl is pointing to the right cluster
kubectl -n cattle-fleet-system logs -l app=fleet-agent
kubectl -n cattle-fleet-system get pods -l app=fleet-agent
```

Additionally you should see a new cluster registered in the Fleet manager.  Below is an example of checking that a new cluster
was registered in the `clusters` [namespace](./namespaces.md).  Please ensure your `${HOME}/.kube/config` is pointed to the Fleet
manager to run this command.

```shell
kubectl -n clusters get clusters.fleet.cattle.io
```
```
NAME                   BUNDLES-READY   NODES-READY   SAMPLE-NODE             LAST-SEEN              STATUS
my-cluster             1/1             1/1           k3d-cluster2-server-0   2020-08-31T19:23:10Z   
```
