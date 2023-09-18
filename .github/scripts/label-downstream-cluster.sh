#!/bin/bash

set -euxo pipefail

ns=${FLEET_E2E_NS_DOWNSTREAM-fleet-default}

{ grep -q -m 1 -e "k3d-downstream"; kill $!; } < <(kubectl get clusters.fleet.cattle.io -n "$ns" -w)
name=$(kubectl get clusters.fleet.cattle.io -o=jsonpath='{.items[0].metadata.name}' -n "$ns")
kubectl patch clusters.fleet.cattle.io -n "$ns" "$name" --type=json -p '[{"op": "add", "path": "/metadata/labels/env", "value": "test" }]'
