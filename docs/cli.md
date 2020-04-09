fleet
===

Fleet is exposed as a pure Kubernetes API using Custom Resources.  The `fleet` is used
only as a way to enhance the experience of interacting with the Bundle custom resources.

## fleet apply [BUNDLE_DIR...]

The apply command will render a bundle resource and then apply it to the cluster.  The
`-o` flag can be used to not apply the resulting YAML but instead save it to a file
or standard out (`-`).

```
Render a bundle into a Kubernetes resource and apply it in the Fleet Manager

Usage:
  fleet apply [flags]

Flags:
  -b, --bundle-file string   Location of the bundle.yaml
  -c, --compress             Force all resources to be compress
  -f, --file string          Read full bundle contents from file
  -h, --help                 help for apply
  -o, --output string        Output contents to file or - for stdout

Global Flags:
  -k, --kubeconfig string   kubeconfig for authentication
  -n, --namespace string    namespace (default "default")
```

## fleet test [BUNDLE_DIR]

The test command is used to simulate matching clusters and rendering the output.  The
entire bundle pipeline will be executed. This means helm and kustomize will be evaluated.
For helm, this is the equivalent of running `helm template` with the same caveauts. That
being that anything that dynamically looks at the cluster will not be proper.  In general
this type of logic should be avoided in most cases.

```
Match a bundle to a target and render the output

Usage:
  fleet test [flags]

Flags:
  -b, --bundle-file string    Location of the bundle.yaml
  -g, --group string          Cluster group to match against
  -L, --group-label strings   Cluster group labels to match against
  -h, --help                  help for test
  -l, --label strings         Cluster labels to match against
      --print-bundle          Don't run match and just output the generated bundle
  -q, --quiet                 Just print the match and don't print the resources
  -t, --target string         Explicit target to match

Global Flags:
  -k, --kubeconfig string   kubeconfig for authentication
  -n, --namespace string    namespace (default "default")

```

## fleet install ...

The install command is for installing the fleet controller and registering clusters
with Fleet.  This command is covered in detail in the [installation documentation](./install.md).
