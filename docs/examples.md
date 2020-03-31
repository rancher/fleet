Examples
========

### Pure Kubernetes YAML

This example uses only Kubernetes YAML that you'd typically see with `kubectl apply`.

[Pure YAML Example](../examples/yaml)

### Helm w/ Embedded Chart

This example shows how to use a Helm chart that is defined locally in the bundle.

[Helm Local Example](../examples/helm-local)

### Helm External Chart

This example shows how to use a chart hosted in a repo from an external source.
This is the most common approach with third party charts.

[Helm Download Example](../examples/helm-download)

### Kustomize

This example shows how to use a pure kustomize approach to deploy bundles

[Kustomize Example](../examples/kustomize)

### Helm w/ Kustomize Post Processing

This example shows how to kustomize a helm chart

[Helm Kustomize Example](../examples/helm-kustomize)
