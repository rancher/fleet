# Expected Repo Structure

A registered git repository should have the following structure

```
./fleet.yaml                # Bundle descriptor (optional)
./manifests/                # Directory for raw kubernetes YAML (if used)
./chart/                    # Directory for an inline Helm Chart (if used)
./kustomize/                # Directory for kustomization resources (if used)
./overlays/${OVERLAY_NAME}  # Directory for customize raw Kubernetes YAML resources (if used)
```

These directories can be configured to different paths using the `fleet.yaml` file. Refer to
the [bundle reference](./bundles.md) documentation on how to customize the behavior.

Also refer to the [examples](./examples.md) to learn how to use raw YAML, Helm, and Kustomize and
how to customize deployments to specific clusters.