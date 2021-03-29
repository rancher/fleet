# Architecture

![](./arch.png)

Fleet has two primary components.  The Fleet manager and the cluster agents.  These
components work in a two-stage pull model.  The Fleet manager will pull from git and the
cluster agents will pull from the Fleet manager.

## Fleet Manager

The Fleet manager is a set of Kubernetes controllers running in any standard Kubernetes
cluster.  The only API exposed by the Fleet manager is the Kubernetes API, there is no
custom API for the fleet controller.

## Cluster Agents

One cluster agent runs in each cluster and is responsible for talking to the Fleet manager.
The only communication from cluster to Fleet manager is by this agent and all communication
goes from the managed cluster to the Fleet manager. The fleet manager does not initiate
connections to downstream clusters. This means managed clusters can run in private networks and behind
NATs. The only requirement is the cluster agent needs to be able to communicate with the
Kubernetes API of the cluster running the Fleet manager. The one exception to this is if you use
the [manager initiated](./manager-initiated.md) cluster registration flow.  This is not required, but
an optional pattern.

The cluster agents are not assumed to have an "always on" connection.  They will resume operation as
soon as they can connect. Future enhancements will probably add the ability to schedule times of when
the agent checks in, as it stands right now they will always attempt to connect.

## Security

The Fleet manager dynamically creates service accounts, manages their RBAC and then gives the
tokens to the downstream clusters. Clusters are registered by optionally expiring cluster registration tokens.
The cluster registration token is used only during the registration process to generate a credential specific
to that cluster. After the cluster credential is established the cluster "forgets" the cluster registration
 token.

The service accounts given to the clusters only have privileges to list `BundleDeployment` in the namespace created
specifically for that cluster. It can also update the `status` subresource of `BundleDeployment` and the `status`
subresource of it's `Cluster` resource.

## Scalability

Fleet is designed to scale up to 1 million clusters. There are more details to come here on how we expect to scale
a Kubernetes, controller-based architecture to 100's of millions of objects and beyond.
