#!/bin/bash

set -euxo pipefail

ns=${FLEET_E2E_NS_DOWNSTREAM-fleet-default}

# { grep -q -m 1 -e "2/2"; kill $!; } < <(kubectl get clusters.fleet.cattle.io -n "$ns" -w)
num_clusters=$(k3d cluster list -o json | jq -r '.[].name | select(. != "upstream")' | wc -l)
while [[ $(kubectl get clusters.fleet.cattle.io -n fleet-default | grep '1/1' | wc -l) -ne $num_clusters ]]; do
    sleep 2
done

for cluster in $(kubectl get clusters.fleet.cattle.io -n "$ns" -o=jsonpath='{.items[*].metadata.name}'); do
  kubectl patch clusters.fleet.cattle.io -n "$ns" "$cluster" --type=json -p '[{"op": "add", "path": "/metadata/labels/env", "value": "test" }]'
done
# name=$(kubectl get clusters.fleet.cattle.io -o=jsonpath='{.items[0].metadata.name}' -n "$ns")
# kubectl patch clusters.fleet.cattle.io -n "$ns" "$name" --type=json -p '[{"op": "add", "path": "/metadata/labels/env", "value": "test" }]'
