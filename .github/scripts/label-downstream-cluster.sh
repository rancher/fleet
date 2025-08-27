#!/bin/bash

set -euxo pipefail

ns=${FLEET_E2E_NS_DOWNSTREAM-fleet-default}

echo "Waiting for downstream cluster to be ready..."
timeout 300 bash -c "until kubectl get clusters.fleet.cattle.io -n '$ns' 2>/dev/null | grep -q '1/1'; do sleep 2; done"

name=$(kubectl get clusters.fleet.cattle.io -o=jsonpath='{.items[0].metadata.name}' -n "$ns")
kubectl patch clusters.fleet.cattle.io -n "$ns" "$name" --type=json -p '[{"op": "add", "path": "/metadata/labels/env", "value": "test" }]'
