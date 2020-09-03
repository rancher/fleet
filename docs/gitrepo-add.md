# Registering

## Proper namespace
Git repos are added to the Fleet manager using the `GitRepo` custom resource type. The
`GitRepo` type is namespaced. If you are using Fleet in a [single cluster](./concepts.md)
style the namespace will always be **fleet-local**. For a [multi-cluster](./concepts.md) style
please ensure you use the correct repo that will map to the right target clusters.

## Create GitRepo instance

Git repositories are register by creating a `GitRepo` following the below YAML sample.  Refer
to the inline comments as the means of each field

```yaml
kind: GitRepo
apiVersion: {{fleet.apiVersion}}
metadata:
  # Any name can be used here, the created bundles will start with this name
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

  # A git repo can produce multiple bundles or maybe your bundle
  # is not at the root of the git repo. The below field is expected
  # to be an array of paths and supports path globbing (ex: some/*/path)
  #
  # Example:
  # bundleDirs:
  # - single-path
  # - multiple-paths/*
  bundleDirs:
  - simple

  # The service account that will be used to perform this deployment.
  # This is the name of the service account that exists in the
  # downstream cluster in the fleet-system namespace. It is assumed
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
