Architecture and Installation
=============================

## Architectures

Fleet has two primary components.  The fleet controller and the cluster agents.

### Fleet Manager

The fleet controller is a set of Kubernetes controllers running in any standard Kubernetes
cluster.  The only API exposed by the Fleet manages is the Kubernetes API, there is no
custom API for the fleet controller.

### Cluster Agents

One cluster agent runs in each cluster and is responsible for talking to the fleet controller.
The only communication from cluster to fleet controller is by this agent and all communication
goes from the managed cluster to the fleet controller.  The fleet controller does not
reach out to the clusters.  This means managed clusters can run in private networks and behind
NAT.  The only requirement is the cluster agent needs to be able to communicate with the
Kubernetes API of the cluster running the fleet controller.

The cluster agents are not assumed to have an "always on" connection.  They will resume operation as
soon as they can connect.  Future enhancements will probably add the ability to schedule times of when
the agent checks in.

### Security

fleet controller dynamically creates service account, manages their RBAC and then gives the
tokens to the clusters. A cluster group can have a series of "Cluster Group Tokens" that
are used to register clusters in to that group. The "cluster group token" is used only during the
registration process to generate a credential specific to that cluster. After the cluster credential
is established the cluster "forgets" the cluster group token.  Cluster group tokens by default have a TTL
of one week.  That can be changed to shorter or to longer to forever.

The service accounts given to the clusters only have privileges to list `BundleDeployment` in the namespace created
specifically for that cluster.  It can also update the `status` subresource of `BundleDeployment`

### Scalability

Fleet is designed to scale up to 1 million clusters. There are more details to come here on how we expect to scale
a Kubernetes controller based architecture to 100's of millions of objects and beyond.

## Installation

### Manager installation

The controller is just a deployment that runs in a Kubernetes cluster.  It is assumed you already have a Kubernetes
cluster available.  The `fleet install controller` command is used to generate a manifest for installation.
The `fleet install controller` command does not need a live connection to a Kubernetes cluster.

```
Generate deployment manifest to run the fleet controller

Usage:
  fleet install controller [flags]

Flags:
      --agent-image string        Image to use for all agents
      --crds-only                 Output CustomResourceDefinitions only
  -h, --help                      help for controller
      --controller-image string      Image to use for controller
      --system-namespace string   Namespace that will be use in controller and agent cluster (default "fleet-system")

Global Flags:
  -k, --kubeconfig string   kubeconfig for authentication
  -n, --namespace string    namespace (default "default")
```

Installation is accomplished typically by doing `fleet install controller | kubectl apply -f -`. The `agent-image` and
`controller-image` fields are important if you wish to run Fleet from a private registry.  The `system-namespace` is the
namespace the fleet controller runs in and also the namespace the cluster agents will run in all clusters.  This is
by default `fleet-system` and it is recommended to keep the default value.

### Cluster Registration

The `fleet install agent-token` and `fleet install agent-config` commands are used to generate Kubernetes manifests to be
used to register clusters. A cluster group token must be generated to register a cluster to the fleet controller.
By default this token will expire in 1 week.  That TTL can be changed.  The cluster group token generated can be
used over and over again while it's still valid to register new clusters.  The `agent-config` command is used to generate
configuration specific to a cluster that you may or may not want to share.  The only functionality at the moment is
to generate the config for a cluster so that on registration it will have specific labels.

The `fleet install agent-token` command requires a live connection to the fleet controller.  Your local `~/.kube/config` is
used by default.

To register a cluster first run `fleet install agent-token` to generate a new token.

```
Generate cluster group token and render manifest to register clusters into a specific cluster group

Usage:
  fleet install agent-token [flags]

Flags:
  -c, --ca-file string            File containing optional CA cert for fleet management server
  -g, --group string              Cluster group to generate config for (default "default")
  -h, --help                      help for agent-token
      --no-ca                     
      --server-url string         The full URL to the fleet management server
      --system-namespace string   System namespace of the controller (default "fleet-system")
  -t, --ttl string                How long the generated registration token is valid, 0 means forever (default "1440m")

Global Flags:
  -k, --kubeconfig string   kubeconfig for authentication
  -n, --namespace string    namespace (default "default")
```

The generated manifest will have information in it that is used to call back to the fleet controller.  By default the 
URL and TLS configuration is taken from your kubeconfig.  Use `--server-url` and `--ca-file` to override those parameters
if they can't be properly derived.

The output of `fleet install agent-token` should be saved to a file you can later apply to a cluster.

```
# Generate token, requires connect to fleet controller
fleet --kubeconfig=fleet-controller-config install agent-token > token
```

If you want to have labels assigned to your cluster during registration this must be done before you apply the token to
the cluster.  The labels are only specified during registration and then after that the cluster can not change it's labels.
The labels can only be changed in the fleet controller.

To generate a configuration with labels run a command like below:

```
fleet install agent-config -l env=prod | kubectl --kubeconfig=cluster-kubeconfig apply -f -
```

Now that you have the custom config setup you can import the token so that the cluster registers

```
kubectl --kubeconfig=cluster-kubeconfig apply -f token
```

### Re-Register/Re-Install Agent

If for any reason your cluster can not connect, you can always generate a new cluster group token and apply it to the
cluster. It will then restart the registration process and generate a new credentials. The identity of the cluster is
determined by the UUID of the `kube-system` namespace so it should reassociate to the cluster previously registered regardless
of the name or labels of the cluster.
