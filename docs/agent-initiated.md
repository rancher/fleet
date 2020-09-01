# Agent Initiated

Refer to the [overview page](./cluster-overview.md#agent-initiated-registration) for a background information on the agent initiated registration style.

## Cluster Registration Token and Client ID

An downstream cluster is registered using two pieces of information, the **cluster registration token** and the **client ID**.

The **cluster registration token** is a credential that will authorize the downstream cluster agent to be
able to initiate the registration process. Refer to the [cluster registration token page](./cluster-tokens.md) for more information
on how to create tokens and obtain the values. The cluster registration token is manifested as a `values.yaml` file that will
be passed to the `helm install` process.

The **client ID** is a unique string that will identify the cluster. This string is user generated and opaque to the Fleet manager and
agent.  It is only assumed to be sufficiently unique. For security reason one should probably not be able to easily guess this value
as then one cluster could impersonate another.  The client ID is optional and if not specific the UID field of the `kube-system` namespace
resource will be used as the client ID. Upon registration if the client ID is found on a `Cluster` resource in the Fleet manager it will associate
the agent with that `Cluster`.  If no `Cluster` resource is found with that client ID a new `Cluster` resource will be created with the specific
client ID.  Client IDs are mostly important such that when a cluster is registered it can immediately be identified, assigned labels, and git
repos can be deployed to it.

## Install agent for registration

The Fleet agent is installed as a Helm chart. The only parameters to the helm chart installation should be the cluster registration token, which
is represented by the `values.yaml` file and the client ID.  The client ID is optional.

First follow the [cluster registration token page](./cluster-tokens.md) to obtain the `values.yaml` file to be used.

Second setup your environment to use use a client ID.

```shell
# If no client ID is going to be used then leave the value blank
CLUSTER_CLIENT_ID="a-unique-value-for-this-cluster"
```

Finally, install the agent using Helm.

!!! hint "Use proper namespace and release name"
    For the agent chart the namespace must be `fleet-system` and the release name `fleet-agent`

!!! hint "Ensure you are installing to the right cluster"
    Helm will use the default context in `${HOME}/.kube/config` to deploy the agent. Use `--kubeconfig` and `--kube-context`
    to change which cluster Helm is installing to.

```shell
helm -n fleet-system install --create-namespace --wait \
    --set clientID="${CLUSTER_CLIENT_ID}" \
    --values values.yaml \
    fleet-agent https://github.com/rancher/fleet/releases/download/{{fleet.version}}/fleet-agent-{{fleet.helmversion}}.tgz
```

The agent should now be deployed.  You can check that status of the fleet pods by running the below commands.

```shell
# Ensure kubectl is pointing to the right cluster
kubectl -n fleet-system logs -l app=fleet-agent
kubectl -n fleet-system get pods -l app=fleet-agent
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

