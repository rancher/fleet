#!/bin/bash

set -euxo pipefail

{ grep -q -m 1 -e "k3d-downstream"; kill $!; } < <(kubectl get clusters.fleet.cattle.io -n ${FLEET_E2E_NS-fleet-default} -w)
name=$(kubectl get clusters.fleet.cattle.io -o=jsonpath='{.items[0].metadata.name}' -n ${FLEET_E2E_NS-fleet-default})
kubectl patch clusters.fleet.cattle.io -n ${FLEET_E2E_NS-fleet-default} "$name" --type=json -p '[{"op": "add", "path": "/metadata/labels/env", "value": "test" }]'
