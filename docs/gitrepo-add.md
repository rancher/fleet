# Registering

## Proper namespace
Git repos are added to the Fleet manager using the `GitRepo` custom resource type. The `GitRepo` type is namespaced. By default, Rancher will create two Fleet workspaces: **fleet-default** and **fleet-local**. 

- `Fleet-default` will contain all the downstream clusters that are already registered through Rancher. 
- `Fleet-local` will contain the local cluster by default. 

Users can create new workspaces and move clusters across workspaces. An example of a special case might be including the local cluster in the `GitRepo` payload for config maps and secrets (no active deployments or payloads).

!!! note "Note:"
    While it's possible to move clusters out of either workspace, we recommend that you keep the local cluster in `fleet-local`.

If you are using Fleet in a [single cluster](./concepts.md) style, the namespace will always be **fleet-local**. Check [here](https://fleet.rancher.io/namespaces/#fleet-local) for more on the `fleet-local` namespace. 

For a [multi-cluster](./concepts.md) style, please ensure you use the correct repo that will map to the right target clusters.

## Create GitRepo instance

Git repositories are register by creating a `GitRepo` following the below YAML sample.  Refer
to the inline comments as the means of each field

```yaml
kind: GitRepo
apiVersion: {{fleet.apiVersion}}
metadata:
  # Any name can be used here
  name: my-repo
  # For single cluster use fleet-local, otherwise use the namespace of
  # your choosing
  namespace: fleet-local
spec:
  # This can be a HTTPS or git URL.  If you are using a git URL then
  # clientSecretName will probably need to be set to supply a credential.
  # repo is the only required parameter for a repo to be monitored.
  #
  repo: https://github.com/rancher/fleet-examples

  # Enforce all resources go to this target namespace. If a cluster scoped
  # resource is found the deployment will fail.
  #
  # targetNamespace: app1

  # Any branch can be watched, this field is optional. If not specified the
  # branch is assumed to be master
  #
  # branch: master

  # A specific commit or tag can also be watched.
  #
  # revision: v0.3.0

  # For a private registry you must supply a clientSecretName. A default
  # secret can be set at the namespace level using the BundleRestriction
  # type. Secrets must be of the type "kubernetes.io/ssh-auth" or
  # "kubernetes.io/basic-auth". The secret is assumed to be in the
  # same namespace as the GitRepo
  #
  # clientSecretName: my-ssh-key
  #
  # If fleet.yaml contains a private Helm repo that requires authentication,
  # provide the credentials in a K8s secret and specify them here. Details are provided
  # in the fleet.yaml documentation.
  #
  # helmSecretName: my-helm-secret
  #
  # To add additional ca-bundle for self-signed certs, caBundle can be 
  # filled with base64 encoded pem data. For example: 
  # `cat /path/to/ca.pem | base64 -w 0` 
  #
  # caBundle: my-ca-bundle
  #
  # Disable SSL verification for git repo
  #
  # insecureSkipTLSVerify: true
  #
  # A git repo can read multiple paths in a repo at once.
  # The below field is expected to be an array of paths and
  # supports path globbing (ex: some/*/path)
  #
  # Example:
  # paths:
  # - single-path
  # - multiple-paths/*
  paths:
  - simple

  # The service account that will be used to perform this deployment.
  # This is the name of the service account that exists in the
  # downstream cluster in the cattle-fleet-system namespace. It is assumed
  # this service account already exists so it should be create before
  # hand, most likely coming from another git repo registered with
  # the Fleet manager.
  #
  # serviceAccount: moreSecureAccountThanClusterAdmin

  # Target clusters to deploy to if running Fleet in a multi-cluster
  # style. Refer to the "Mapping to Downstream Clusters" docs for
  # more information.
  #
  # targets: ...
```

## Adding private repository

Fleet supports both http and ssh auth key for private repository. To use this you have to create a secret in the same namespace. 

For example, to generate a private ssh key

```text
ssh-keygen -t rsa -b 4096 -m pem -C "user@email.com"
```

Note: The private key format has to be in `EC PRIVATE KEY`, `RSA PRIVATE KEY` or `PRIVATE KEY` and should not contain a passphase. 

Put your private key into secret:

```text
kubectl create secret generic $name -n $namespace --from-file=ssh-privatekey=/file/to/private/key  --type=kubernetes.io/ssh-auth 
```

!!! note
    Private key with passphrase is not supported.

Fleet supports putting `known_hosts` into ssh secret. Here is an example of how to add it:

Fetch the public key hash(take github as an example)

```text
ssh-keyscan -H github.com
```

And add it into secret:

```text
apiVersion: v1
kind: Secret
metadata:
  name: ssh-key
type: kubernetes.io/ssh-auth
stringData:
  ssh-privatekey: <private-key>
  known_hosts: |-
    |1|YJr1VZoi6dM0oE+zkM0do3Z04TQ=|7MclCn1fLROZG+BgR4m1r8TLwWc= ssh-rsa AAAAB3NzaC1yc2EAAAABIwAAAQEAq2A7hRGmdnm9tUDbO9IDSwBK6TbQa+PXYPCPy6rbTrTtw7PHkccKrpp0yVhp5HdEIcKr6pLlVDBfOLX9QUsyCOV0wzfjIJNlGEYsdlLJizHhbn2mUjvSAHQqZETYP81eFzLQNnPHt4EVVUh7VfDESU84KezmD5QlWpXLmvU31/yMf+Se8xhHTvKSCZIFImWwoG6mbUoWf9nzpIoaSjB+weqqUUmpaaasXVal72J+UX2B+2RPW3RcT0eOzQgqlJL3RKrTJvdsjE3JEAvGq3lGHSZXy28G3skua2SmVi/w4yCE6gbODqnTWlg7+wC604ydGXA8VJiS5ap43JXiUFFAaQ==
```

!!! note
    If you don't add it any server's public key will be trusted and added. (`ssh -o stricthostkeychecking=accept-new` will be used)

!!! note
    If you are using openssh format for the private key and you are creating it in the UI, make sure a carriage return is appended in the end of the private key.

# Troubleshooting

See Fleet Troubleshooting section [here](./troubleshooting.md).
