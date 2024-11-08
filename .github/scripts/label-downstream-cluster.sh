#!/bin/bash

set -euxo pipefail

ns=${FLEET_E2E_NS_DOWNSTREAM-fleet-default}

# Wait for clusters to become "ready" by waiting for bundles to become ready.
num_clusters=$(k3d cluster list -o json | jq -r '.[].name | select( . | contains("downstream") )' | wc -l)
while [[ $(kubectl get clusters.fleet.cattle.io -n "$ns" | grep '1/1' -c) -ne $num_clusters ]]; do
    sleep 1
done

for cluster in $(kubectl get clusters.fleet.cattle.io -n "$ns" -o=jsonpath='{.items[*].metadata.name}'); do
  kubectl patch clusters.fleet.cattle.io -n "$ns" "$cluster" --type=json -p '[{"op": "add", "path": "/metadata/labels/env", "value": "test" }]'
done
