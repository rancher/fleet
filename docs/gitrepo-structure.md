# Expected Repo Structure

**The git repository has no explicitly required structure.** It is important
to realize the scanned resources will be saved as a resource in Kubernetes so
you want to make sure the directories you are scanning in git do not contain
arbitrarily large resources. Right now there is a limitation that the resources
deployed must **gzip to less than 1MB**.

## How repos are scanned

Multiple paths can be defined for a `GitRepo` and each path is scanned independently.
Internally each scanned path will become a [bundle](./concepts.md) that Fleet will manage,
deploy, and monitor independently.

The following files are looked for to determine the how the resources will be deployed.

| File | Location | Meaning |
|------|----------|---------|
| **Chart.yaml**:| / relative to `path` or custom path from `fleet.yaml` | The resources will be deployed as a Helm chart. Refer to the `fleet.yaml` for more options. |
| **kustomization.yaml**:| / relative to `path` or custom path from `fleet.yaml` | The resources will be deployed using Kustomize. Refer to the `fleet.yaml` for more options. |
| **fleet.yaml** | Any subpath | If any fleet.yaml is found a new [bundle](./concepts.md) will be defined. This allows mixing charts, kustomize, and raw YAML in the same repo |
| ** *.yaml ** | Any subpath | If a `Chart.yaml` or `kustomization.yaml` is not found then any `.yaml` or `.yml` file will be assumed to be a Kubernetes resource and will be deployed. |
| **overlays/{name}** | / relative to `path` | When deploying using raw YAML (not Kustomize or Helm) `overlays` is a special directory for customizations. |

## `fleet.yaml`

The `fleet.yaml` is an optional file that can be included in the git repository to change the behavior of how
the resources are deployed and customized.  The `fleet.yaml` is always at the root relative to the `path` of the `GitRepo`
and if a subdirectory is found with a `fleet.yaml` a new [bundle](./concepts.md) is defined that will then be
configured differently from the parent bundle.

!!! warning "Helm chart dependencies"
    It is up to the user to fulfill the dependency list for the Helm charts. As such, you must manually run `helm dependencies update $chart` OR run `helm dependencies build $chart` prior to install. See the [Fleet docs](https://rancher.com/docs/rancher/v2.6/en/deploy-across-clusters/fleet/#helm-chart-dependencies) in Rancher for more information.

### Reference

!!! tip "How changes are applied to `values.yaml`"

    - Note that the most recently applied changes to the `values.yaml` will override any previously existing values.

    - When changes are applied to the `values.yaml` from multiple sources at the same time, the values will update in the following order: `helmValues` -> `helm.valuesFiles` -> `helm.valuesFrom`.

```yaml
# The default namespace to be applied to resources. This field is not used to
# enforce or lock down the deployment to a specific namespace, but instead
# provide the default value of the namespace field if one is not specified
# in the manifests.
# Default: default
defaultNamespace: default

# All resources will be assigned to this namespace and if any cluster scoped
# resource exists the deployment will fail.
# Default: ""
namespace: default

kustomize:
  # Use a custom folder for kustomize resources. This folder must contain
  # a kustomization.yaml file.
  dir: ./kustomize

helm:
  # Use a custom location for the Helm chart. This can refer to any go-getter URL or
  # OCI registry based helm chart URL e.g. "oci://ghcr.io/fleetrepoci/guestbook".
  # This allows one to download charts from most any location.  Also know that
  # go-getter URL supports adding a digest to validate the download. If repo
  # is set below this field is the name of the chart to lookup
  chart: ./chart
  # A https URL to a Helm repo to download the chart from. It's typically easier
  # to just use `chart` field and refer to a tgz file.  If repo is used the
  # value of `chart` will be used as the chart name to lookup in the Helm repository.
  repo: https://charts.rancher.io
  # A custom release name to deploy the chart as. If not specified a release name
  # will be generated.
  releaseName: my-release
  # The version of the chart or semver constraint of the chart to find. If a constraint
  # is specified it is evaluated each time git changes.
  # The version also determines which chart to download from OCI registries.
  version: 0.1.0
  # Any values that should be placed in the `values.yaml` and passed to helm during
  # install.
  values:
    any-custom: value
  # All labels on Rancher clusters are available using global.fleet.clusterLabels.LABELNAME
  # These can now be accessed directly as variables
    variableName: global.fleet.clusterLabels.LABELNAME
  # Path to any values files that need to be passed to helm during install
  valuesFiles:
    - values1.yaml
    - values2.yaml
  # Allow to use values files from configmaps or secrets
  valuesFrom:
  - configMapKeyRef:
      name: configmap-values
      # default to namespace of bundle
      namespace: default 
      key: values.yaml
    secretKeyRef:
      name: secret-values
      namespace: default
      key: values.yaml
  # Override immutable resources. This could be dangerous.
  force: false
  # Set the Helm --atomic flag when upgrading
  atomic: false

# A paused bundle will not update downstream clusters but instead mark the bundle
# as OutOfSync. One can then manually confirm that a bundle should be deployed to
# the downstream clusters.
# Default: false
paused: false

rolloutStrategy:
    # A number or percentage of clusters that can be unavailable during an update
    # of a bundle. This follows the same basic approach as a deployment rollout
    # strategy. Once the number of clusters meets unavailable state update will be
    # paused. Default value is 100% which doesn't take effect on update.
    # default: 100%
    maxUnavailable: 15%
    # A number or percentage of cluster partitions that can be unavailable during
    # an update of a bundle.
    # default: 0
    maxUnavailablePartitions: 20%
    # A number of percentage of how to automatically partition clusters if not
    # specific partitioning strategy is configured.
    # default: 25%
    autoPartitionSize: 10%
    # A list of definitions of partitions.  If any target clusters do not match
    # the configuration they are added to partitions at the end following the
    # autoPartitionSize.
    partitions:
      # A user friend name given to the partition used for Display (optional).
      # default: ""
    - name: canary
      # A number or percentage of clusters that can be unavailable in this
      # partition before this partition is treated as done.
      # default: 10%
      maxUnavailable: 10%
      # Selector matching cluster labels to include in this partition
      clusterSelector:
        matchLabels:
          env: prod
      # A cluster group name to include in this partition
      clusterGroup: agroup
      # Selector matching cluster group labels to include in this partition
      clusterGroupSelector: agroup
      
# Target customization are used to determine how resources should be modified per target
# Targets are evaluated in order and the first one to match a cluster is used for that cluster.
targetCustomizations:
# The name of target. If not specified a default name of the format "target000"
# will be used. This value is mostly for display
- name: prod
  # Custom namespace value overriding the value at the root
  namespace: newvalue
  # Custom defaultNamespace value overriding the value at the root
  defaultNamespace: newdefaultvalue
  # Custom kustomize options overriding the options at the root
  kustomize: {}
  # Custom Helm options override the options at the root
  helm: {}
  # If using raw YAML these are names that map to overlays/{name} that will be used
  # to replace or patch a resource. If you wish to customize the file ./subdir/resource.yaml
  # then a file ./overlays/myoverlay/subdir/resource.yaml will replace the base file.
  # A file named ./overlays/myoverlay/subdir/resource_patch.yaml will patch the base file.
  # A patch can in JSON Patch or JSON Merge format or a strategic merge patch for builtin
  # Kubernetes types. Refer to "Raw YAML Resource Customization" below for more information.
  yaml:
    overlays:
    - custom2
    - custom3
  # A selector used to match clusters.  The structure is the standard
  # metav1.LabelSelector format. If clusterGroupSelector or clusterGroup is specified,
  # clusterSelector will be used only to further refine the selection after
  # clusterGroupSelector and clusterGroup is evaluated.
  clusterSelector:
    matchLabels:
      env: prod
  # A selector used to match a specific cluster by name.    
  clusterName: dev-cluster    
  # A selector used to match cluster groups.
  clusterGroupSelector:
    matchLabels:
      region: us-east
  # A specific clusterGroup by name that will be selected
  clusterGroup: group1

# dependsOn allows you to configure dependencies to other bundles. The current bundle
# will only be deployed, after all dependencies are deployed and in a Ready state.
dependsOn:
  # Format: <GITREPO-NAME>-<BUNDLE_PATH> with all path separators replaced by "-" 
  # Example: GitRepo name "one", Bundle path "/multi-cluster/hello-world" => "one-multi-cluster-hello-world"
  - name: one-multi-cluster-hello-world
```

!!! hint "Private Helm Repo"
    For a private Helm repo, users can reference a secret with the following keys:
    
    1. `username` and `password` for basic http auth if the Helm HTTP repo is behind basic auth.
    
    2. `cacerts` for custom CA bundle if the Helm repo is using a custom CA.
    
    3. `ssh-privatekey` for ssh private key if repo is using ssh protocol. Private key with passphase is not supported currently.
    
    For example, to add a secret in kubectl, run 
    
    `kubectl create secret -n $namespace generic helm --from-literal=username=foo --from-literal=password=bar --from-file=cacerts=/path/to/cacerts --from-file=ssh-privatekey=/path/to/privatekey.pem`
    
    After secret is created, specify the secret to `gitRepo.spec.helmSecretName`. Make sure secret is created under the same namespace with gitrepo.

### Using ValuesFrom

These examples showcase the style and format for using `valuesFrom`.

Example [ConfigMap](https://kubernetes.io/docs/concepts/configuration/configmap/):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: configmap-values
  namespace: default
data:  
  values.yaml: |-
    replication: true
    replicas: 2
    serviceType: NodePort
```

Example [Secret](https://kubernetes.io/docs/concepts/configuration/secret/):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: secret-values
  namespace: default
stringData:
  values.yaml: |-
    replication: true
    replicas: 2
    serviceType: NodePort
```

## Per Cluster Customization

The `GitRepo` defines which clusters a git repository should be deployed to and the `fleet.yaml` in the repository
determines how the resources are customized per target.

All clusters and cluster groups in the same namespace as the `GitRepo` will be evaluated against all targets of that
`GitRepo`. The targets list is evaluated one by one and if there is a match the resource will be deployed to the cluster.
If no match is made against the target list on the `GitRepo` then the resources will not be deployed to that cluster.
Once a target cluster is matched the `fleet.yaml` from the git repository is then consulted for customizations. The
`targetCustomizations` in the `fleet.yaml` will be evaluated one by one and the first match will define how the
resource is to be configured. If no match is made the resources will be deployed with no additional customizations.

There are three approaches to matching clusters for both `GitRepo` `targets` and `fleet.yaml` `targetCustomizations`.
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

## Raw YAML Resource Customization

When using Kustomize or Helm the `kustomization.yaml` or the `helm.values` will control how the resource are
customized per target cluster. If you are using raw YAML then the following simple mechanism is built-in and can
be used.  The `overlays/` folder in the git repo is treated specially as folder containing folders that
can be selected to overlay on top per target cluster. The resource overlay content
uses a file name based approach.  This is different from kustomize which uses a resource based approach.  In kustomize
the resource Group, Kind, Version, Name, and Namespace identify resources and are then merged or patched.  For Fleet
the overlay resources will override or patch content with a matching file name.

```shell
# Base files
deployment.yaml
svc.yaml

# Overlay files

# The following file we be added
overlays/custom/configmap.yaml
# The following file will replace svc.yaml
overlays/custom/svc.yaml
# The following file will patch deployment.yaml
overlays/custom/deployment_patch.yaml
```

A file named `foo` will replace a file called `foo` from the base resources or a previous overlay.  In order to patch
the contents a file the convention of adding `_patch.` (notice the trailing period) to the filename is used. The string `_patch.`
will be replaced with `.` from the file name and that will be used as the target.  For example `deployment_patch.yaml`
will target `deployment.yaml`.  The patch will be applied using JSON Merge, Strategic Merge Patch, or JSON Patch.
Which strategy is used is based on the file content. Even though JSON strategies are used, the files can be written
using YAML syntax.

## Cluster and Bundle state

See [Cluster and Bundle state](./cluster-bundles-state.md).
